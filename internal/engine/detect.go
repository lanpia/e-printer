package engine

import (
	"bytes"
	"os"
	"strings"
)

// Format 은 입력 문서의 페이지 기술 언어(PDL)를 나타낸다.
type Format int

const (
	FormatUnknown Format = iota
	FormatPDF
	FormatPS  // PostScript / EPS
	FormatPCL // PCL5 등 ESC 기반
	FormatPXL // PCL-XL (바이너리)
)

func (f Format) String() string {
	switch f {
	case FormatPDF:
		return "PDF"
	case FormatPS:
		return "PostScript"
	case FormatPCL:
		return "PCL"
	case FormatPXL:
		return "PCL-XL"
	default:
		return "Unknown"
	}
}

// 어떤 백엔드 엔진을 써야 하는지 알려준다.
func (f Format) NeedsGhostPCL() bool { return f == FormatPCL || f == FormatPXL }

// DetectFile 은 파일 앞부분을 읽어 형식을 추정한다.
func DetectFile(path string) (Format, error) {
	f, err := os.Open(path)
	if err != nil {
		return FormatUnknown, err
	}
	defer f.Close()

	head := make([]byte, 4096)
	n, _ := f.Read(head)
	return detectBytes(head[:n], path), nil
}

func detectBytes(head []byte, path string) Format {
	// PDF: 보통 "%PDF-" 로 시작 (앞에 잡바이트가 끼는 경우 대비해 검색)
	if i := bytes.Index(head, []byte("%PDF-")); i >= 0 && i < 1024 {
		return FormatPDF
	}
	// PJL 헤더로 시작하면 안쪽 LANGUAGE 지시자를 본다.
	//   \x1b%-12345X @PJL ... ENTER LANGUAGE = PCLXL / POSTSCRIPT
	upper := bytes.ToUpper(head)
	if bytes.HasPrefix(head, []byte("\x1b%-12345X")) || bytes.Contains(upper, []byte("@PJL")) {
		switch {
		case bytes.Contains(upper, []byte("LANGUAGE=PCLXL")),
			bytes.Contains(upper, []byte("LANGUAGE = PCLXL")):
			return FormatPXL
		case bytes.Contains(upper, []byte("LANGUAGE=POSTSCRIPT")),
			bytes.Contains(upper, []byte("LANGUAGE = POSTSCRIPT")):
			return FormatPS
		}
		// PJL 만 있고 언어 미지정이면 ESC E(PCL5) 유무로 판단, 없으면 PCL 로 간주.
		return FormatPCL
	}
	// PCL-XL 시그니처: ") HP-PCL XL"
	if bytes.Contains(head, []byte(") HP-PCL XL")) {
		return FormatPXL
	}
	// PostScript / EPS: "%!"
	if bytes.HasPrefix(head, []byte("%!")) {
		return FormatPS
	}
	// 순수 PCL5: ESC 로 시작 (리셋 시퀀스 ESC E 등)
	if len(head) > 0 && head[0] == 0x1b {
		return FormatPCL
	}
	// 마지막으로 확장자 힌트.
	switch strings.ToLower(extOf(path)) {
	case ".pdf":
		return FormatPDF
	case ".ps", ".eps":
		return FormatPS
	case ".pcl":
		return FormatPCL
	case ".pxl", ".px3":
		return FormatPXL
	}
	return FormatUnknown
}

func extOf(path string) string {
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		return path[i:]
	}
	return ""
}
