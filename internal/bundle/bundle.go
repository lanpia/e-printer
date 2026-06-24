// Package bundle 는 Ghostscript/GhostPCL 엔진 일체를 실행파일 안에 임베드하고,
// 첫 실행 시 사용자 캐시 폴더로 풀어 self-contained 동작을 보장한다.
//
// 임베드된 engines.zip 내부 구조:
//
//	engines/gs/bin/gswin64c.exe, gsdll64.dll, lib/, Resource/, iccprofiles/
//	engines/pcl/gpcl6win64.exe, gpcl6dll64.dll
package bundle

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

//go:embed engines.zip
var enginesZip []byte

// 캐시 키. 엔진 버전이 바뀌면 이 값을 올려 재추출을 유도한다.
const bundleVersion = "gs10071-ghostpcl10071-v1"

// Paths 는 추출된 백엔드 실행파일 경로이다.
type Paths struct {
	Ghostscript string // engines/gs/bin/gswin64c.exe
	GhostPCL    string // engines/pcl/gpcl6win64.exe
}

// Ensure 는 임베드 엔진을 캐시 폴더로 (필요시) 풀고 경로를 돌려준다.
// 이미 풀려 있으면 추출을 건너뛴다.
func Ensure() (Paths, error) {
	root, err := cacheRoot()
	if err != nil {
		return Paths{}, err
	}
	p := Paths{
		Ghostscript: filepath.Join(root, "engines", "gs", "bin", "gswin64c.exe"),
		GhostPCL:    filepath.Join(root, "engines", "pcl", "gpcl6win64.exe"),
	}
	marker := filepath.Join(root, ".extracted")
	if fileExists(marker) && fileExists(p.Ghostscript) && fileExists(p.GhostPCL) {
		return p, nil // 이미 준비됨
	}
	if err := extractAll(root); err != nil {
		return Paths{}, fmt.Errorf("엔진 추출 실패: %w", err)
	}
	if err := os.WriteFile(marker, []byte(bundleVersion), 0o644); err != nil {
		return Paths{}, err
	}
	return p, nil
}

// 캐시 루트: %LOCALAPPDATA%\eprinter\<버전>
func cacheRoot() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	root := filepath.Join(base, "eprinter", bundleVersion)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	return root, nil
}

// 임베드 zip 을 root 아래로 전부 푼다(경로 탈출 방지).
func extractAll(root string) error {
	zr, err := zip.NewReader(bytes.NewReader(enginesZip), int64(len(enginesZip)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		// 일부 압축도구(PowerShell Compress-Archive 등)는 항목 경로에 역슬래시를
		// 쓴다. 정규화해 구분자/디렉터리 판별이 깨지지 않게 한다.
		name := strings.ReplaceAll(f.Name, `\`, "/")
		dst := filepath.Join(root, filepath.FromSlash(name))
		// Zip Slip 방어
		if !withinRoot(root, dst) {
			return fmt.Errorf("잘못된 경로: %s", f.Name)
		}
		if strings.HasSuffix(name, "/") || f.FileInfo().IsDir() {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := writeFile(f, dst); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(f *zip.File, dst string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

func withinRoot(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !startsWithDotDot(rel)
}

func startsWithDotDot(rel string) bool {
	return len(rel) >= 2 && rel[0] == '.' && rel[1] == '.'
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
