package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"eprinter/internal/engine"
	"eprinter/internal/vprinter"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// guiMain 은 walk 기반 데스크톱 UI 를 띄운다.
func guiMain() {
	// walk UI 는 단일 OS 스레드에 고정되어야 한다(로딩창과 본 창이 같은 스레드여야
	// Synchronize/메시지 전달이 올바르게 동작한다).
	runtime.LockOSThread()

	eng := resolveEngines()

	// 가상 프린터를 먼저 설치한 뒤(로딩창 표시) 본 GUI 를 띄운다.
	admin := vprinter.IsAdmin()
	var installErr error
	if admin {
		installErr = runInstallSplash()
	}

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
	watcher := &vprinter.Watcher{OutputDir: autoDir}

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
								watcher.OutputDir = d
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
	// 설치 자체는 본 창 생성 전에 runInstallSplash 에서 이미 끝났다.
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
		watcher.Convert = func(in, out string) error {
			return eng.Convert(context.Background(), engine.ConvertOptions{
				Input: in, Output: out, Kind: engine.OutPDF,
			})
		}
		watcher.OnEvent = appendLog
		go watcher.Run()
		mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
			watcher.Stop()
			_ = vprinter.Remove()
		})
	}

	mw.Run()
}

// runInstallSplash 는 가상 프린터를 설치하는 동안 로딩창을 띄우고,
// 설치가 끝나면 로딩창을 닫은 뒤 설치 결과(err)를 반환한다. (관리자 권한에서만 호출)
//
// 설치(PowerShell)는 별도 고루틴에서 돌리고, 로딩창의 메시지 루프(sp.Run)가
// 그동안 화면을 그려 "멈춘 창"처럼 보이지 않게 한다. walk 의 MainWindow 는
// 닫혀도 PostQuitMessage 를 보내지 않으므로, 이 창을 닫은 뒤 본 창을 띄워도 된다.
func runInstallSplash() error {
	var sp *walk.MainWindow
	if err := (MainWindow{
		AssignTo: &sp,
		Title:    "eprinter 준비 중",
		MinSize:  Size{Width: 380, Height: 120},
		Size:     Size{Width: 380, Height: 120},
		Layout:   VBox{Margins: Margins{Left: 16, Top: 16, Right: 16, Bottom: 16}},
		Children: []Widget{
			Label{Text: "가상 프린터 \"eprinter\" 를 설치하는 중입니다…"},
			Label{Text: "잠시만 기다려 주세요."},
		},
	}).Create(); err != nil {
		// 로딩창을 만들지 못하면 그냥 설치만 한다.
		return vprinter.Install()
	}

	// 설치가 끝나기 전에는 사용자가 로딩창을 닫지 못하게 막는다.
	// (조기 닫힘 시 sp.Run 이 먼저 반환하면서 정리/신호 순서가 꼬이는 레이스를 원천 차단)
	// installing 은 UI 스레드에서만 읽고 쓴다(Closing 핸들러, 그리고 아래 Synchronize 콜백).
	installing := true
	sp.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if installing {
			*canceled = true
		}
	})

	done := make(chan error, 1)
	go func() {
		var err error
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("설치 중 오류: %v", r)
			}
			// 먼저 로딩창 정리를 큐에 넣어 sp 접근을 끝낸 뒤에 완료를 신호한다.
			sp.Synchronize(func() {
				installing = false
				sp.Close()
			})
			done <- err
		}()
		err = vprinter.Install()
	}()

	sp.Run() // 설치가 끝나 sp.Close() 가 불릴 때까지 메시지 루프를 돌린다.
	return <-done
}

// defaultSaveDir 은 자동 저장 폴더 기본값(바탕화면 → 사용자 폴더)을 반환한다.
func defaultSaveDir() string {
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
