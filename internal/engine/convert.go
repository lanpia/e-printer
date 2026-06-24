package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// OutputKind 는 변환 결과물 종류이다.
type OutputKind int

const (
	OutPDF OutputKind = iota
	OutPNG
	OutJPEG
	OutTIFF
	OutText // 텍스트 추출 (txtwrite)
)

// 확장자로 출력 종류를 추정한다.
func OutputKindFromExt(path string) (OutputKind, error) {
	switch strings.ToLower(extOf(path)) {
	case ".pdf":
		return OutPDF, nil
	case ".png":
		return OutPNG, nil
	case ".jpg", ".jpeg":
		return OutJPEG, nil
	case ".tif", ".tiff":
		return OutTIFF, nil
	case ".txt":
		return OutText, nil
	default:
		return OutPDF, fmt.Errorf("출력 확장자를 알 수 없음: %q (.pdf/.png/.jpg/.tif/.txt 지원)", extOf(path))
	}
}

// Ghostscript/GhostPCL 공용 device 이름. 두 엔진 모두 동일한 device 군을 공유한다.
func (k OutputKind) device() string {
	switch k {
	case OutPNG:
		return "png16m"
	case OutJPEG:
		return "jpeg"
	case OutTIFF:
		return "tiff24nc"
	case OutText:
		return "txtwrite"
	default:
		return "pdfwrite"
	}
}

// 래스터 출력 여부 (해상도 -r 옵션 적용 대상).
func (k OutputKind) isRaster() bool {
	return k == OutPNG || k == OutJPEG || k == OutTIFF
}

// ConvertOptions 는 변환 파라미터이다.
type ConvertOptions struct {
	Input   string
	Output  string
	Kind    OutputKind
	DPI     int    // 래스터 출력 해상도. 0이면 기본 300.
	Pages   string // 예) "1-3,5". 비어 있으면 전체.
	Verbose bool
}

// Convert 는 입력 형식을 감지해 알맞은 엔진으로 변환을 수행한다.
func (e Engines) Convert(ctx context.Context, opt ConvertOptions) error {
	format, err := DetectFile(opt.Input)
	if err != nil {
		return err
	}
	bin, err := e.binFor(format)
	if err != nil {
		return err
	}
	args := buildArgs(format, opt)
	if opt.Verbose {
		fmt.Fprintf(os.Stderr, "[입력형식] %s\n[엔진] %s\n[명령] %s %s\n",
			format, bin, bin, strings.Join(args, " "))
	}
	return run(ctx, bin, args)
}

// 형식에 맞는 백엔드 바이너리를 고른다.
func (e Engines) binFor(f Format) (string, error) {
	if f.NeedsGhostPCL() {
		if e.GhostPCL == "" {
			return "", fmt.Errorf("PCL/PCL-XL 처리에는 GhostPCL(gpcl6win64.exe)이 필요합니다 — 미탐지")
		}
		return e.GhostPCL, nil
	}
	if e.Ghostscript == "" {
		return "", fmt.Errorf("PDF/PostScript 처리에는 Ghostscript(gswin64c.exe)가 필요합니다 — 미탐지")
	}
	return e.Ghostscript, nil
}

// Ghostscript/GhostPCL 공통 인자 구성.
func buildArgs(format Format, opt ConvertOptions) []string {
	dpi := opt.DPI
	if dpi == 0 {
		dpi = 300
	}
	args := []string{
		"-dNOPAUSE", "-dBATCH", "-dSAFER", "-q",
		"-sDEVICE=" + opt.Kind.device(),
	}
	if opt.Kind.isRaster() {
		args = append(args, fmt.Sprintf("-r%d", dpi))
	}
	if rng := pageRangeArgs(opt.Pages); len(rng) > 0 {
		args = append(args, rng...)
	}
	// 래스터 다중 페이지는 %d 패턴이 필요하다(out-001.png ...).
	out := opt.Output
	if opt.Kind.isRaster() && !strings.Contains(out, "%d") {
		out = insertPagePattern(out)
	}
	args = append(args, "-o", out)

	args = append(args, personalityArgs(format)...)
	args = append(args, opt.Input)
	return args
}

// GhostPCL(-l)의 personality 인자. 유효값은 PCL5C/PCL5E/RTL/PCLXL 뿐이며
// "PCL" 같은 값은 거부된다. Ghostscript(PDF/PS)에는 해당 옵션이 없으므로 생략.
func personalityArgs(format Format) []string {
	switch format {
	case FormatPCL:
		return []string{"-lPCL5C"} // 컬러 포함 상위 호환 모드
	case FormatPXL:
		return []string{"-lPCLXL"}
	default:
		return nil
	}
}

// "1-3,5" → -dFirstPage/-dLastPage (단순 연속 범위만 정확 지원, 콤마는 첫 토큰 사용)
func pageRangeArgs(spec string) []string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil
	}
	first := spec
	if i := strings.IndexByte(spec, ','); i >= 0 {
		first = spec[:i]
	}
	if i := strings.IndexByte(first, '-'); i >= 0 {
		lo, hi := strings.TrimSpace(first[:i]), strings.TrimSpace(first[i+1:])
		var out []string
		if lo != "" {
			out = append(out, "-dFirstPage="+lo)
		}
		if hi != "" {
			out = append(out, "-dLastPage="+hi)
		}
		return out
	}
	return []string{"-dFirstPage=" + first, "-dLastPage=" + first}
}

// out.png → out-%03d.png
func insertPagePattern(out string) string {
	if i := strings.LastIndexByte(out, '.'); i >= 0 {
		return out[:i] + "-%03d" + out[i:]
	}
	return out + "-%03d"
}

// run 은 외부 프로세스를 실행하고 표준출력/에러를 그대로 전달한다.
func run(ctx context.Context, bin string, args []string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s 실행 실패: %w", bin, err)
	}
	return nil
}
