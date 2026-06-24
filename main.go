// eprinter — "모두의프린터" 핵심 변환/출력 엔진의 Go 클론.
//
// 단일 실행파일로 CLI 와 GUI 를 모두 제공하며, 백엔드 엔진
// (Ghostscript / GhostPCL)까지 exe 안에 임베드되어 별도 설치가 필요 없다.
//
//   - 인자 없이 실행(더블클릭) → GUI
//   - 인자와 함께 실행          → CLI
//
// CLI 사용 예)
//
//	eprinter info
//	eprinter printers
//	eprinter convert -i in.pcl -o out.pdf
//	eprinter convert -i in.pdf  -o page.png -dpi 200 -pages 1-3
//	eprinter print   -i in.pdf  -p "Microsoft Print to PDF" -copies 2
package main

import (
	"os"
	"runtime"

	"eprinter/internal/bundle"
	"eprinter/internal/engine"
	"eprinter/internal/vprinter"
)

func main() {
	// 인자가 있으면 CLI, 없으면 GUI.
	if len(os.Args) >= 2 {
		attachConsole() // 콘솔에서 실행된 경우 표준출력을 부모 콘솔에 붙인다.
		runCLI(os.Args[1:])
		return
	}
	// GUI 는 가상 프린터를 설치/제거하므로 관리자 권한이 필요하다.
	// 권한이 없으면 UAC 로 자기 자신을 재실행하고 현재 프로세스는 종료한다.
	ensureElevatedForGUI()
	guiMain()
}

// ensureElevatedForGUI 는 GUI 실행에 필요한 관리자 권한을 확보한다.
// 권한이 없으면 관리자로 재실행을 시도하고, 성공하면 현재(비권한) 프로세스를 종료한다.
// 재실행 실패(UAC 거부 등) 시엔 그대로 진행한다 — 수동 변환은 여전히 가능하다.
func ensureElevatedForGUI() {
	if runtime.GOOS != "windows" || vprinter.IsAdmin() {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if err := vprinter.RunElevated(exe); err == nil {
		os.Exit(0)
	}
}

// resolveEngines 는 엔진을 결정한다.
// 우선순위: 환경변수/시스템 설치(engine.Locate) → 임베드 번들(bundle.Ensure).
// 시스템에 설치돼 있으면 그걸 쓰고, 없으면 exe 에 동봉된 엔진을 풀어 쓴다.
func resolveEngines() engine.Engines {
	e := engine.Locate()
	if e.Ghostscript == "" || e.GhostPCL == "" {
		if p, err := bundle.Ensure(); err == nil {
			if e.Ghostscript == "" {
				e.Ghostscript = p.Ghostscript
			}
			if e.GhostPCL == "" {
				e.GhostPCL = p.GhostPCL
			}
		}
	}
	return e
}
