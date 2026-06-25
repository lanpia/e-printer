package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"eprinter/internal/bundle"
	"eprinter/internal/engine"
	"eprinter/internal/vprinter"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"golang.org/x/sys/windows/registry"
)

// guiMain 은 walk 기반 데스크톱 UI 를 띄운다.
func guiMain() {
	// walk UI 는 단일 OS 스레드에 고정되어야 한다(로딩창과 본 창이 같은 스레드여야
	// Synchronize/메시지 전달이 올바르게 동작한다).
	runtime.LockOSThread()

	// 로딩창을 띄운 채로 무거운 준비(엔진 추출 + 관리자면 가상 프린터 설치)를 끝낸다.
	// 그래야 프린터가 처음부터 목록/상태에 반영되고, 캐시 재추출 지연도 창에 가려진다.
	admin := vprinter.IsAdmin()
	eng, installErr := runStartupSplash(admin)

	var mw *walk.MainWindow
	var status *walk.Label
	var vpLabel *walk.Label
	var autoEdit *walk.LineEdit

	setStatus := func(format string, a ...interface{}) {
		if status != nil {
			status.SetText(fmt.Sprintf(format, a...))
		}
	}

	autoDir := defaultSaveDir()
	srv := &vprinter.Server{OutputDir: autoDir}

	if err := (MainWindow{
		AssignTo: &mw,
		Title:    "나효미의 프린터 — PDF 변환 / 인쇄",
		MinSize:  Size{Width: 440, Height: 150},
		Size:     Size{Width: 520, Height: 200},
		Layout:   VBox{},
		Children: []Widget{
			GroupBox{
				Title:  "가상 프린터",
				Layout: Grid{Columns: 3},
				Children: []Widget{
					Label{Text: "상태:"},
					Label{AssignTo: &vpLabel, Text: "초기화 중…", ColumnSpan: 2},
					Label{Text: "자동 저장 폴더:"},
					LineEdit{AssignTo: &autoEdit, Text: autoDir, ReadOnly: true},
					PushButton{
						Text: "폴더…",
						OnClicked: func() {
							if d := folderDialog(mw, autoEdit.Text()); d != "" {
								autoEdit.SetText(d)
								srv.OutputDir = d
								setStatus("자동 저장 폴더: %s", d)
							}
						},
					},
				},
			},
			GroupBox{
				Title:  "백엔드 엔진 (내장)",
				Layout: VBox{},
				Children: []Widget{
					Label{Text: engineStatus(eng)},
				},
			},
			Label{AssignTo: &status, Text: "준비됨."},
		},
	}).Create(); err != nil {
		walk.MsgBox(nil, "오류", "창 생성 실패: "+err.Error(), walk.MsgBoxIconError)
		os.Exit(1)
	}

	// 가상 프린터 상태 표시 + 스풀 감시 시작 + 종료 시 정리(a안).
	// 엔진 준비/프린터 설치는 본 창 생성 전에 runStartupSplash 에서 이미 끝났다.
	appendLog := func(msg string) {
		mw.Synchronize(func() {
			setStatus("%s", msg)
		})
	}
	switch {
	case !admin:
		vpLabel.SetText("비활성 — 관리자 권한이 없어 가상 프린터를 설치하지 못했습니다")
	case installErr != nil:
		vpLabel.SetText("설치 실패")
		_ = vprinter.Remove() // 포트만 생기고 프린터 추가가 실패한 부분 설치 잔여물 정리(idempotent)
		walk.MsgBox(mw, "가상 프린터 설치 실패", installErr.Error(), walk.MsgBoxIconWarning)
	default:
		vpLabel.SetText(fmt.Sprintf("활성 — \"%s\" 로 인쇄하면 자동으로 PDF 저장됩니다", vprinter.PrinterName))
		srv.Save = func(data []byte, out string) error {
			// 기본 드라이버(Microsoft Print To PDF)는 받은 데이터가 이미 완성된 PDF다.
			// 그대로 저장해 색·텍스트(유니코드)를 보존한다.
			if bytes.HasPrefix(data, []byte("%PDF")) {
				return os.WriteFile(out, data, 0o644)
			}
			// 폴백(PS/PCL 드라이버): 임시 파일로 쓴 뒤 Ghostscript 로 PDF 변환.
			tmp := out + ".spool"
			if err := os.WriteFile(tmp, data, 0o644); err != nil {
				return err
			}
			defer os.Remove(tmp)
			return eng.Convert(context.Background(), engine.ConvertOptions{
				Input: tmp, Output: out, Kind: engine.OutPDF,
			})
		}
		srv.OnEvent = appendLog
		if err := srv.Start(); err != nil {
			vpLabel.SetText("리스너 시작 실패 — 다른 프로그램이 포트를 쓰는지 확인하세요")
			walk.MsgBox(mw, "리스너 오류", err.Error(), walk.MsgBoxIconWarning)
		}
		// 종료 시: 리스너 중지 + 프린터/포트/드라이버 원복.
		mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
			srv.Stop()
			_ = vprinter.Remove()
		})
	}

	mw.Run()

	// 종료: 추출했던 엔진 캐시(%LOCALAPPDATA%\eprinter)까지 삭제해 흔적을 남기지 않는다.
	// (프린터·포트·드라이버·스풀 폴더는 위 Closing 의 vprinter.Remove 에서 이미 정리됨)
	_ = bundle.RemoveCache()
}

// runStartupSplash 는 로딩창을 띄운 채로 무거운 시작 작업을 끝낸다:
// 엔진 추출(resolveEngines)과, admin 이면 가상 프린터 설치(vprinter.Install).
// 작업이 끝나면 로딩창을 닫고 (탐지된 엔진, 설치 오류)를 반환한다.
//
// 무거운 작업은 별도 고루틴에서 돌리고 로딩창의 메시지 루프(sp.Run)가 그동안
// 화면을 그려 "멈춘 창"처럼 보이지 않게 한다. walk 의 MainWindow 는 닫혀도
// PostQuitMessage 를 보내지 않으므로, 이 창을 닫은 뒤 본 창을 띄워도 된다.
func runStartupSplash(admin bool) (engine.Engines, error) {
	var eng engine.Engines
	var installErr error
	doWork := func() {
		eng = resolveEngines() // 캐시 없으면 임베드 엔진을 추출(수 초 걸릴 수 있음)
		if admin {
			installErr = vprinter.Install()
		}
	}

	var sp *walk.MainWindow
	if err := (MainWindow{
		AssignTo: &sp,
		Title:    "eprinter 준비 중",
		MinSize:  Size{Width: 380, Height: 120},
		Size:     Size{Width: 380, Height: 120},
		Layout:   VBox{Margins: Margins{Left: 16, Top: 16, Right: 16, Bottom: 16}},
		Children: []Widget{
			Label{Text: "eprinter 를 준비하는 중입니다…"},
			Label{Text: "엔진 준비 및 가상 프린터 설치 중 — 잠시만 기다려 주세요."},
		},
	}).Create(); err != nil {
		doWork() // 로딩창을 못 만들면 그냥 진행
		return eng, installErr
	}

	// 작업이 끝나기 전에는 사용자가 로딩창을 닫지 못하게 막는다.
	// (조기 닫힘 시 sp.Run 이 먼저 반환하면서 정리/신호 순서가 꼬이는 레이스를 원천 차단)
	// installing 은 UI 스레드에서만 읽고 쓴다(Closing 핸들러 + 아래 Synchronize 콜백).
	installing := true
	sp.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if installing {
			*canceled = true
		}
	})

	done := make(chan struct{}, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil && installErr == nil {
				installErr = fmt.Errorf("준비 중 오류: %v", r)
			}
			// 먼저 로딩창 정리를 큐에 넣어 sp 접근을 끝낸 뒤에 완료를 신호한다.
			sp.Synchronize(func() {
				installing = false
				sp.Close()
			})
			done <- struct{}{}
		}()
		doWork()
	}()

	sp.Run() // 작업이 끝나 sp.Close() 가 불릴 때까지 메시지 루프를 돌린다.
	<-done
	return eng, installErr
}

// defaultSaveDir 은 PDF 자동 저장 기본 폴더(항상 바탕화면)를 반환한다.
// OneDrive 등으로 리다이렉트된 바탕화면도 정확히 잡기 위해 레지스트리의
// 실제 Desktop 경로를 먼저 읽고, 실패 시 %USERPROFILE%\Desktop 으로 폴백한다.
func defaultSaveDir() string {
	if k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Explorer\Shell Folders`,
		registry.QUERY_VALUE); err == nil {
		defer k.Close()
		if v, _, err := k.GetStringValue("Desktop"); err == nil && v != "" {
			if fi, err := os.Stat(v); err == nil && fi.IsDir() {
				return v
			}
		}
	}
	if h := os.Getenv("USERPROFILE"); h != "" {
		desk := filepath.Join(h, "Desktop")
		if fi, err := os.Stat(desk); err == nil && fi.IsDir() {
			return desk
		}
		return h
	}
	return "."
}

// folderDialog 는 폴더 선택 대화상자를 띄운다.
func folderDialog(owner walk.Form, current string) string {
	dlg := &walk.FileDialog{
		Title:    "자동 저장 폴더 선택",
		FilePath: current,
	}
	if ok, _ := dlg.ShowBrowseFolder(owner); ok {
		return dlg.FilePath
	}
	return ""
}

// engineStatus 는 내장 엔진 사용 가능 여부를 짧은 한 줄로 표시한다.
func engineStatus(eng engine.Engines) string {
	switch {
	case eng.Ghostscript != "" && eng.GhostPCL != "":
		return "사용 가능 ✓ (Ghostscript + GhostPCL 내장)"
	case eng.Ghostscript == "" && eng.GhostPCL == "":
		return "엔진 미탐지 ✗"
	case eng.Ghostscript == "":
		return "GhostPCL만 사용 가능 (Ghostscript 미탐지)"
	default:
		return "Ghostscript만 사용 가능 (GhostPCL 미탐지)"
	}
}
