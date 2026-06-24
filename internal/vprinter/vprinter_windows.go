//go:build windows

// Package vprinter 는 "모두의프린터" 가상 프린터를 Windows 에 설치/제거한다.
//
// 동작 원리(RedMon 없이, 외부 의존성 0):
//   - PostScript 드라이버에 바인딩된 "로컬 파일 포트"를 가진 프린터를 만든다.
//   - 그 프린터로 인쇄하면 스풀 데이터(PostScript)가 포트 파일로 떨어진다.
//   - 앱이 그 파일을 감시(Watch)하다가 Ghostscript 로 PDF 변환해 저장한다.
//
// 프린터/포트 설치·제거는 관리자 권한이 필요하므로, IsAdmin/RunElevated 로
// 권한을 확보한 뒤 호출해야 한다.
package vprinter

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// PrinterName 은 시스템에 표시될 가상 프린터 이름이다.
// (한글 이름은 일부 드라이버/대화상자에서 인코딩이 깨지므로 ASCII 로 둔다.)
const PrinterName = "eprinter"

// SpoolDir 은 스풀 파일이 모이는 폴더(%ProgramData%\eprinter\spool)이다.
//
// 주의: 사용자 프로필(%LOCALAPPDATA% 등) 아래 경로는 쓸 수 없다. 인쇄 스풀러는
// SYSTEM 권한으로 동작하며, 보안 강화(PrintNightmare 대응)로 사용자 프로필
// 경로를 가리키는 로컬 포트 생성을 거부한다. 시스템 공용 위치인 ProgramData 를 쓴다.
func SpoolDir() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "eprinter", "spool")
}

// PortFile 은 로컬 포트가 가리키는 파일 경로이다. 인쇄 시 이 파일로 스풀된다.
func PortFile() string {
	return filepath.Join(SpoolDir(), "eprinter.ps")
}

// IsAdmin 은 현재 프로세스가 관리자 권한인지 반환한다.
func IsAdmin() bool {
	shell32 := syscall.NewLazyDLL("shell32.dll")
	r, _, _ := shell32.NewProc("IsUserAnAdmin").Call()
	return r != 0
}

// RunElevated 는 지정한 실행파일을 관리자 권한(UAC)으로 다시 실행한다.
// 호출한 프로세스는 그대로 두므로, 호출 측에서 종료 여부를 결정한다.
func RunElevated(exe string, args ...string) error {
	const swShowNormal = 1
	verbPtr, _ := syscall.UTF16PtrFromString("runas")
	exePtr, _ := syscall.UTF16PtrFromString(exe)
	var paramPtr *uint16
	if len(args) > 0 {
		paramPtr, _ = syscall.UTF16PtrFromString(quoteArgs(args))
	}
	shell32 := syscall.NewLazyDLL("shell32.dll")
	r, _, _ := shell32.NewProc("ShellExecuteW").Call(
		0,
		uintptr(unsafe.Pointer(verbPtr)),
		uintptr(unsafe.Pointer(exePtr)),
		uintptr(unsafe.Pointer(paramPtr)),
		0,
		swShowNormal,
	)
	if r <= 32 { // ShellExecute 는 성공 시 32 초과 값을 반환한다.
		return fmt.Errorf("관리자 권한 실행 실패(코드 %d) — UAC 거부 가능", r)
	}
	return nil
}

// psQuote 는 문자열을 PowerShell 작은따옴표 리터럴로 감싼다.
//
// 반드시 작은따옴표를 써야 한다. 큰따옴표(또는 Go의 %q)로 감싸면 Windows 경로의
// 백슬래시가 이중(\\)으로 들어가 "잘못된 경로명"(0x8007007b) 오류가 난다.
// PowerShell 작은따옴표 안에서는 작은따옴표만 두 개로 이스케이프하면 된다.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// Installed 는 가상 프린터가 현재 설치돼 있는지 반환한다.
func Installed() bool {
	out, err := runPS(fmt.Sprintf(
		`if (Get-Printer -Name %s -ErrorAction SilentlyContinue) { 'yes' } else { 'no' }`,
		psQuote(PrinterName)))
	return err == nil && strings.Contains(out, "yes")
}

// Install 은 가상 프린터를 설치한다(관리자 권한 필요).
// 이미 있으면 깨끗이 제거 후 다시 설치한다.
func Install() error {
	if err := os.MkdirAll(SpoolDir(), 0o755); err != nil {
		return fmt.Errorf("스풀 폴더 생성 실패: %w", err)
	}
	driver, err := ensureDriver()
	if err != nil {
		return err
	}
	port := PortFile()

	// PowerShell 한 번에: 잔여물 정리 → 포트 추가 → 프린터 추가.
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$name = %s
$port = %s
$driver = %s
# 잔여 프린터/포트 정리(없으면 무시)
Get-Printer -Name $name -ErrorAction SilentlyContinue | Remove-Printer -ErrorAction SilentlyContinue
Get-PrinterPort -Name $port -ErrorAction SilentlyContinue | Remove-PrinterPort -ErrorAction SilentlyContinue
# 로컬 파일 포트 + 프린터 생성
Add-PrinterPort -Name $port
Add-Printer -Name $name -DriverName $driver -PortName $port
# 컬러 기본값(GDI 경로 보강용). 미지원 드라이버는 무시.
try { Set-PrintConfiguration -PrinterName $name -Color $true -ErrorAction Stop } catch {}
'OK'
`, psQuote(PrinterName), psQuote(port), psQuote(driver))

	out, err := runPS(script)
	if err != nil {
		return fmt.Errorf("프린터 설치 실패: %v\n%s", err, out)
	}
	return nil
}

// Remove 는 가상 프린터와 포트를 제거한다(관리자 권한 필요).
func Remove() error {
	script := fmt.Sprintf(`
$name = %s
$port = %s
Get-Printer -Name $name -ErrorAction SilentlyContinue | Remove-Printer -ErrorAction SilentlyContinue
Get-PrinterPort -Name $port -ErrorAction SilentlyContinue | Remove-PrinterPort -ErrorAction SilentlyContinue
'OK'
`, psQuote(PrinterName), psQuote(PortFile()))
	out, err := runPS(script)
	if err != nil {
		return fmt.Errorf("프린터 제거 실패: %v\n%s", err, out)
	}
	return nil
}

// msPSDriver 는 우선 사용할 인박스 PostScript 드라이버 이름이다.
//
// 이 드라이버를 쓰는 이유: Samsung Universal Print Driver 같은 일부 벤더
// PostScript 드라이버는 색을 항상 회색조로 스풀하고(PageOutputColor=Grayscale),
// private devmode 스냅샷이 색 설정 변경을 도로 덮어써서 컬러로 못 바꾼다.
// 인박스 "Microsoft PS Class Driver" 는 문서의 색을 그대로 컬러로 스풀하며
// 모든 Windows 10/11 에 기본 제공되어 이식성도 좋다.
const msPSDriver = "Microsoft PS Class Driver"

// ensureDriver 는 설치에 사용할 PostScript 드라이버를 결정한다.
// 우선순위: 환경변수 강제 지정 → 인박스 Microsoft PS Class Driver(필요시 자동 설치)
// → 시스템에 이미 있는 PostScript 드라이버 스캔(pickPSDriver).
func ensureDriver() (string, error) {
	if d := strings.TrimSpace(os.Getenv("EPRINTER_PRINTER_DRIVER")); d != "" {
		return d, nil
	}
	// 인박스 Microsoft PS Class Driver 를 드라이버 저장소에서 스테이징(이미 있으면 그대로).
	out, err := runPS(fmt.Sprintf(`
$d = %s
if (-not (Get-PrinterDriver -Name $d -ErrorAction SilentlyContinue)) {
  try { Add-PrinterDriver -Name $d -ErrorAction Stop } catch {}
}
if (Get-PrinterDriver -Name $d -ErrorAction SilentlyContinue) { 'OK' } else { 'NO' }
`, psQuote(msPSDriver)))
	if err == nil && strings.Contains(out, "OK") {
		return msPSDriver, nil
	}
	// 폴백: 시스템에 이미 설치된 PostScript 드라이버를 찾는다.
	return pickPSDriver()
}

// pickPSDriver 는 설치에 사용할 PostScript 드라이버 이름을 고른다(폴백용).
func pickPSDriver() (string, error) {
	if d := strings.TrimSpace(os.Getenv("EPRINTER_PRINTER_DRIVER")); d != "" {
		return d, nil
	}
	// 이름에 PostScript/ PS 가 들어가는 드라이버를 우선 선택.
	out, err := runPS(`Get-PrinterDriver | Select-Object -ExpandProperty Name`)
	if err != nil {
		return "", fmt.Errorf("드라이버 목록 조회 실패: %w", err)
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			names = append(names, s)
		}
	}
	for _, n := range names {
		u := strings.ToUpper(n)
		if strings.Contains(u, "POSTSCRIPT") || strings.HasSuffix(u, " PS") ||
			strings.HasSuffix(u, "PS") || strings.Contains(u, " PS ") {
			return n, nil
		}
	}
	return "", fmt.Errorf("PostScript 드라이버를 찾지 못했습니다. "+
		"환경변수 EPRINTER_PRINTER_DRIVER 로 드라이버 이름을 지정하세요. "+
		"(설치된 드라이버: %s)", strings.Join(names, ", "))
}

// runPS 는 PowerShell 스크립트를 실행하고 표준출력을 반환한다.
// 출력 인코딩을 UTF-8 로 고정해 한글이 깨지지 않게 한다(기본 OEM 코드페이지 회피).
func runPS(script string) (string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"[Console]::OutputEncoding=[System.Text.Encoding]::UTF8; "+script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
