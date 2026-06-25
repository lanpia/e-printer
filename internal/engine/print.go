package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// PrintOptions 는 인쇄 파라미터이다.
type PrintOptions struct {
	Input   string
	Printer string // 비어 있으면 기본 프린터(프린터 선택 대화상자 없이 기본 사용)
	Copies  int    // 0이면 1부
	Pages   string
	Verbose bool
}

// Print 는 문서를 Windows 프린터로 직접 출력한다.
// Ghostscript/GhostPCL 의 mswinpr2 device 를 사용해 스풀러로 보낸다.
func (e Engines) Print(ctx context.Context, opt PrintOptions) error {
	format, err := DetectFile(opt.Input)
	if err != nil {
		return err
	}
	bin, err := e.binFor(format)
	if err != nil {
		return err
	}
	copies := opt.Copies
	if copies <= 0 {
		copies = 1
	}

	args := []string{
		"-dNOPAUSE", "-dBATCH", "-dSAFER", "-q",
		"-dNumCopies=" + fmt.Sprint(copies),
		"-sDEVICE=mswinpr2",
		"-dNoCancel", // 진행률 대화상자 취소버튼 숨김
	}
	if opt.Printer != "" {
		// 공백 포함 프린터명을 위해 따옴표는 exec 가 처리하므로 값만 넣는다.
		args = append(args, fmt.Sprintf(`-sOutputFile=%%printer%%%s`, opt.Printer))
	} // 비우면 mswinpr2 가 기본 프린터로 보냄(대화상자 없이).
	if rng := pageRangeArgs(opt.Pages); len(rng) > 0 {
		args = append(args, rng...)
	}
	args = append(args, personalityArgs(format)...)
	args = append(args, opt.Input)

	if opt.Verbose {
		fmt.Fprintf(os.Stderr, "[입력형식] %s\n[엔진] %s\n[프린터] %s\n[부수] %d\n",
			format, bin, printerLabel(opt.Printer), copies)
	}
	return run(ctx, bin, args)
}

func printerLabel(p string) string {
	if p == "" {
		return "(기본 프린터)"
	}
	return p
}

// ListPrinters 는 설치된 프린터 목록을 반환한다(Windows, PowerShell 사용).
func ListPrinters(ctx context.Context) ([]string, error) {
	// 외부 의존성 없이 PowerShell 의 Get-Printer 를 호출한다.
	// 한글 프린터 이름이 깨지지 않도록 출력 인코딩을 UTF-8 로 고정한다.
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive",
		"-Command", "[Console]::OutputEncoding=[System.Text.Encoding]::UTF8; "+
			"Get-Printer | Select-Object -ExpandProperty Name")
	hideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("프린터 목록 조회 실패: %w", err)
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			names = append(names, s)
		}
	}
	return names, nil
}
