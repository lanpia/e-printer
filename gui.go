package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"eprinter/internal/engine"
	"eprinter/internal/vprinter"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// guiMain 은 walk 기반 데스크톱 UI 를 띄운다.
func guiMain() {
	eng := resolveEngines()
	printers := loadPrinters()

	var mw *walk.MainWindow
	var inEdit, outEdit *walk.LineEdit
	var printerCb *walk.ComboBox
	var status *walk.Label
	var vpLabel *walk.Label
	var autoEdit *walk.LineEdit
	var logBox *walk.TextEdit

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
		MinSize:  Size{Width: 560, Height: 320},
		Layout:   VBox{},
		Children: []Widget{
			GroupBox{
				Title:  "백엔드 엔진 (실행파일에 내장됨)",
				Layout: Grid{Columns: 2},
				Children: []Widget{
					Label{Text: "Ghostscript:"},
					Label{Text: pathOrMissing(eng.Ghostscript)},
					Label{Text: "GhostPCL:"},
					Label{Text: pathOrMissing(eng.GhostPCL)},
				},
			},
			GroupBox{
				Title:  "가상 프린터 (다른 프로그램에서 \"eprinter\"로 인쇄 → 자동 PDF 저장)",
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
				Title:  "프린터",
				Layout: HBox{},
				Children: []Widget{
					Label{Text: "설치된 프린터:"},
					ComboBox{
						AssignTo:     &printerCb,
						Model:        printers,
						CurrentIndex: 0,
						MinSize:      Size{Width: 280},
					},
					PushButton{
						Text: "새로고침",
						OnClicked: func() {
							printerCb.SetModel(loadPrinters())
							setStatus("프린터 목록을 새로고침했습니다.")
						},
					},
				},
			},
			GroupBox{
				Title:  "입력 문서 (PDF / PostScript / PCL / PCL-XL)",
				Layout: HBox{},
				Children: []Widget{
					LineEdit{AssignTo: &inEdit, ReadOnly: true},
					PushButton{
						Text: "찾아보기…",
						OnClicked: func() {
							if p := openDialog(mw); p != "" {
								inEdit.SetText(p)
								if outEdit.Text() == "" {
									outEdit.SetText(suggestPDF(p))
								}
								setStatus("입력: %s", filepath.Base(p))
							}
						},
					},
				},
			},
			GroupBox{
				Title:  "PDF 저장 위치 / 파일명",
				Layout: HBox{},
				Children: []Widget{
					LineEdit{AssignTo: &outEdit},
					PushButton{
						Text: "저장 위치…",
						OnClicked: func() {
							if p := saveDialog(mw, outEdit.Text()); p != "" {
								outEdit.SetText(p)
							}
						},
					},
				},
			},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					PushButton{
						Text:      "PDF로 변환",
						OnClicked: func() { doConvert(mw, eng, inEdit.Text(), outEdit.Text(), setStatus) },
					},
					PushButton{
						Text:      "선택한 프린터로 인쇄",
						OnClicked: func() { doPrint(mw, eng, inEdit.Text(), printerCb.Text(), setStatus) },
					},
					HSpacer{},
				},
			},
			Label{Text: "변환 기록:"},
			TextEdit{AssignTo: &logBox, ReadOnly: true, VScroll: true, MinSize: Size{Height: 90}},
			Label{AssignTo: &status, Text: "준비됨."},
		},
	}).Create(); err != nil {
		walk.MsgBox(nil, "오류", "창 생성 실패: "+err.Error(), walk.MsgBoxIconError)
		os.Exit(1)
	}

	// 가상 프린터 라이프사이클(a안): 실행 시 설치 / 종료 시 삭제.
	startVirtualPrinter(mw, eng, watcher, vpLabel, logBox, setStatus)

	mw.Run()
}

// startVirtualPrinter 는 가상 프린터를 설치하고 스풀 감시를 시작한다(a안).
// 창이 닫힐 때 프린터를 제거하도록 Closing 핸들러를 건다.
func startVirtualPrinter(mw *walk.MainWindow, eng engine.Engines, watcher *vprinter.Watcher,
	vpLabel *walk.Label, logBox *walk.TextEdit, setStatus func(string, ...interface{})) {

	appendLog := func(msg string) {
		mw.Synchronize(func() {
			logBox.AppendText(msg + "\r\n")
			setStatus(msg)
		})
	}

	if !vprinter.IsAdmin() {
		vpLabel.SetText("비활성 — 관리자 권한이 없어 가상 프린터를 설치하지 못했습니다 (수동 변환만 가능)")
		return
	}

	if err := vprinter.Install(); err != nil {
		vpLabel.SetText("설치 실패 — 수동 변환만 가능")
		walk.MsgBox(mw, "가상 프린터 설치 실패", err.Error(), walk.MsgBoxIconWarning)
		return
	}
	vpLabel.SetText(fmt.Sprintf("활성 — \"%s\" 로 인쇄하면 자동으로 PDF 저장됩니다", vprinter.PrinterName))

	// 스풀 감시 시작.
	watcher.Convert = func(in, out string) error {
		return eng.Convert(context.Background(), engine.ConvertOptions{
			Input: in, Output: out, Kind: engine.OutPDF,
		})
	}
	watcher.OnEvent = appendLog
	go watcher.Run()

	// 종료 시 정리.
	mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		watcher.Stop()
		_ = vprinter.Remove()
	})
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

func doConvert(mw *walk.MainWindow, eng engine.Engines, in, out string, setStatus func(string, ...interface{})) {
	if in == "" {
		walk.MsgBox(mw, "확인", "입력 문서를 먼저 선택하세요.", walk.MsgBoxIconWarning)
		return
	}
	if out == "" {
		walk.MsgBox(mw, "확인", "PDF 저장 경로/파일명을 지정하세요.", walk.MsgBoxIconWarning)
		return
	}
	if !strings.EqualFold(filepath.Ext(out), ".pdf") {
		out += ".pdf"
	}
	setStatus("변환 중…")
	err := eng.Convert(context.Background(), engine.ConvertOptions{
		Input: in, Output: out, Kind: engine.OutPDF,
	})
	if err != nil {
		setStatus("실패")
		walk.MsgBox(mw, "변환 실패", err.Error(), walk.MsgBoxIconError)
		return
	}
	setStatus("완료: %s", out)
	walk.MsgBox(mw, "완료", "PDF로 저장했습니다:\n"+out, walk.MsgBoxIconInformation)
}

func doPrint(mw *walk.MainWindow, eng engine.Engines, in, printer string, setStatus func(string, ...interface{})) {
	if in == "" {
		walk.MsgBox(mw, "확인", "입력 문서를 먼저 선택하세요.", walk.MsgBoxIconWarning)
		return
	}
	if printer == "" || strings.HasPrefix(printer, "(") {
		walk.MsgBox(mw, "확인", "사용할 프린터를 선택하세요.", walk.MsgBoxIconWarning)
		return
	}
	setStatus("인쇄 중… (%s)", printer)
	err := eng.Print(context.Background(), engine.PrintOptions{
		Input: in, Printer: printer, Copies: 1,
	})
	if err != nil {
		setStatus("인쇄 실패")
		walk.MsgBox(mw, "인쇄 실패", err.Error(), walk.MsgBoxIconError)
		return
	}
	setStatus("인쇄 전송 완료: %s", printer)
}

func loadPrinters() []string {
	names, err := engine.ListPrinters(context.Background())
	if err != nil || len(names) == 0 {
		return []string{"(프린터 없음)"}
	}
	return names
}

func openDialog(owner walk.Form) string {
	dlg := &walk.FileDialog{
		Title:  "입력 문서 선택",
		Filter: "지원 문서 (*.pdf;*.ps;*.eps;*.pcl;*.pxl)|*.pdf;*.ps;*.eps;*.pcl;*.pxl|모든 파일 (*.*)|*.*",
	}
	if ok, _ := dlg.ShowOpen(owner); ok {
		return dlg.FilePath
	}
	return ""
}

func saveDialog(owner walk.Form, current string) string {
	dlg := &walk.FileDialog{
		Title:    "PDF 저장",
		Filter:   "PDF 파일 (*.pdf)|*.pdf",
		FilePath: current,
	}
	if ok, _ := dlg.ShowSave(owner); ok {
		p := dlg.FilePath
		if !strings.EqualFold(filepath.Ext(p), ".pdf") {
			p += ".pdf"
		}
		return p
	}
	return ""
}

func suggestPDF(in string) string {
	ext := filepath.Ext(in)
	return strings.TrimSuffix(in, ext) + ".pdf"
}

func pathOrMissing(s string) string {
	if s == "" {
		return "(미탐지)"
	}
	return s
}
