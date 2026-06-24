package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Engines 는 탐색된 백엔드 실행파일 경로를 담는다.
//   - Ghostscript : PostScript / PDF 해석 (gswin64c.exe)
//   - GhostPCL    : HP PCL / PCL-XL 해석 (gpcl6win64.exe)
type Engines struct {
	Ghostscript string // 비어 있으면 미탐지
	GhostPCL    string
}

// 환경변수로 직접 지정할 수 있도록 한다(자동 탐색보다 우선).
const (
	envGS  = "EPRINTER_GS"
	envPCL = "EPRINTER_GHOSTPCL"
)

// 자동 탐색 후보 경로. {root} 패턴 디렉터리는 버전 폴더를 와일드카드로 훑는다.
func gsCandidates() []string {
	var c []string
	if v := os.Getenv(envGS); v != "" {
		c = append(c, v)
	}
	c = append(c, fromPath("gswin64c.exe", "gswin64.exe", "gswin32c.exe", "gs")...)
	// C:\Program Files\gs\gs10.07.1\bin\gswin64c.exe 형태
	for _, base := range programDirs("gs") {
		c = append(c, globVersioned(base, "bin", "gswin64c.exe")...)
		c = append(c, globVersioned(base, "bin", "gswin32c.exe")...)
	}
	return c
}

func pclCandidates() []string {
	var c []string
	if v := os.Getenv(envPCL); v != "" {
		c = append(c, v)
	}
	c = append(c, fromPath("gpcl6win64.exe", "gpcl6win32.exe", "gpcl6")...)
	// 동봉된 zip 을 푼 경우: ...\ghostpcl-10.07.1-win64\gpcl6win64.exe
	home, _ := os.UserHomeDir()
	roots := []string{".", filepath.Join(home, "Desktop"), `C:\`, `C:\Program Files`}
	for _, r := range roots {
		c = append(c, globMatch(r, "ghostpcl-*-win64", "gpcl6win64.exe")...)
		c = append(c, globMatch(r, "**", "gpcl6win64.exe")...)
	}
	return c
}

// Locate 는 사용 가능한 백엔드를 탐색해 반환한다.
func Locate() Engines {
	return Engines{
		Ghostscript: firstExisting(gsCandidates()),
		GhostPCL:    firstExisting(pclCandidates()),
	}
}

// --- 헬퍼 ---

// PATH 상의 실행파일을 절대경로로 풀어준다.
func fromPath(names ...string) []string {
	var out []string
	dirs := filepath.SplitList(os.Getenv("PATH"))
	for _, n := range names {
		for _, d := range dirs {
			out = append(out, filepath.Join(d, n))
		}
	}
	return out
}

func programDirs(sub string) []string {
	var bases []string
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
		if v := os.Getenv(env); v != "" {
			bases = append(bases, filepath.Join(v, sub))
		}
	}
	if len(bases) == 0 {
		bases = append(bases, filepath.Join(`C:\Program Files`, sub))
	}
	return bases
}

// base\<버전폴더>\parts... 를 글롭으로 찾되 최신 버전이 앞에 오도록 내림차순 정렬.
func globVersioned(base string, parts ...string) []string {
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dirs))) // gs10.07.1 > gs9.56 ...
	var out []string
	for _, d := range dirs {
		out = append(out, filepath.Join(append([]string{base, d}, parts...)...))
	}
	return out
}

// dir 아래에서 patternDir(글롭) 안의 file 을 찾는다. patternDir=="**" 면 1단계 하위까지 훑는다.
func globMatch(dir, patternDir, file string) []string {
	if patternDir == "**" {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil
		}
		var out []string
		for _, e := range entries {
			if e.IsDir() {
				out = append(out, filepath.Join(dir, e.Name(), file))
			}
		}
		return out
	}
	matches, _ := filepath.Glob(filepath.Join(dir, patternDir, file))
	return matches
}

func firstExisting(paths []string) string {
	for _, p := range paths {
		if p == "" {
			continue
		}
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// String 은 사람이 읽기 좋은 탐지 요약을 만든다.
func (e Engines) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Ghostscript : %s\n", orMissing(e.Ghostscript))
	fmt.Fprintf(&b, "GhostPCL    : %s\n", orMissing(e.GhostPCL))
	return b.String()
}

func orMissing(s string) string {
	if s == "" {
		return "(미탐지 — 설치 후 PATH 추가하거나 환경변수 지정)"
	}
	return s
}
