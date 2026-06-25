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

// portName 은 가상 프린터가 쓰는 Standard TCP/IP 포트의 이름이다.
// 포트는 루프백(127.0.0.1:TCPPort)을 가리키고, 앱의 로컬 리스너가 그걸 받는다.
//
// TCP/IP 포트를 쓰는 이유: ReportShop 등 일부 인쇄앱은 "로컬 *파일* 포트"를
// 지원하지 않는 포트로 거부하고, 로컬/TCP-IP 포트만 허용한다.
const portName = "eprinter_tcp"

// baseDir 은 앱이 시스템에 만드는 작업 폴더(%ProgramData%\eprinter)이다.
// 드라이버 설치 표식 등을 둔다(종료 시 통째로 정리).
func baseDir() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "eprinter")
}

// driverMarker 는 "이 앱이 드라이버를 새로 설치했음"을 기록하는 표식 파일이다.
// 이 표식이 있으면 종료 시 그 드라이버를 제거해 원래 상태로 되돌린다.
// (원래 시스템에 있던 드라이버라면 표식을 남기지 않아 보존된다.)
func driverMarker() string {
	return filepath.Join(baseDir(), "driver-added.marker")
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
// 포트는 루프백을 가리키는 Standard TCP/IP 포트이고, 인쇄 데이터는 앱의
// 로컬 리스너(Server)가 받는다. 이미 있으면 깨끗이 제거 후 다시 설치한다.
func Install() error {
	driver, err := ensureDriver()
	if err != nil {
		return err
	}

	// PowerShell 한 번에: 잔여물 정리 → TCP/IP 포트 추가 → 프린터 추가 → 색 컬러 고정.
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$name = %s
$port = %s
$driver = %s
# 잔여 프린터 정리(없으면 무시)
Get-Printer -Name $name -ErrorAction SilentlyContinue | Remove-Printer -ErrorAction SilentlyContinue
# 루프백 Standard TCP/IP(RAW) 포트 — 이미 있으면 재사용한다.
# (TCP/IP 포트는 깔끔히 제거되지 않는 경우가 있어, 매번 재생성하면 'already exists'(0x800700b7)로 실패한다.
#  포트 설정은 항상 127.0.0.1:고정포트라 잔여 포트를 그대로 써도 안전하다.)
if (-not (Get-PrinterPort -Name $port -ErrorAction SilentlyContinue)) {
  Add-PrinterPort -Name $port -PrinterHostAddress %s -PortNumber %d
}
Add-Printer -Name $name -DriverName $driver -PortName $port
# 색을 컬러로 고정(드라이버 기본이 흑백이어도). 둘 다 best-effort.
try { Set-PrintConfiguration -PrinterName $name -Color $true -ErrorAction Stop } catch {}
try {
  Add-Type -AssemblyName System.Printing -ErrorAction Stop
  $srv = New-Object System.Printing.LocalPrintServer
  $admin = [System.Printing.PrintSystemDesiredAccess]::AdministratePrinter
  $pq = New-Object System.Printing.PrintQueue($srv, $name, $admin)
  $tk = $pq.DefaultPrintTicket
  $tk.OutputColor = [System.Printing.OutputColor]::Color
  $pq.DefaultPrintTicket = $tk
  $pq.Commit()
  $pq.Dispose()
} catch {}
'OK'
`, psQuote(PrinterName), psQuote(portName), psQuote(driver), psQuote(tcpHost), TCPPort())

	out, err := runPS(script)
	if err != nil {
		return fmt.Errorf("프린터 설치 실패: %v\n%s", err, out)
	}
	return nil
}

// Remove 는 가상 프린터·포트를 제거하고, 우리가 설치한 드라이버·작업 폴더까지
// 정리해 시스템을 원래 상태로 되돌린다(관리자 권한 필요).
func Remove() error {
	// 우리가 드라이버를 새로 설치했던 경우에만, 그때 설치한 바로 그 드라이버를 제거한다.
	// (표식 파일에 우리가 추가한 드라이버 이름이 들어 있다.)
	driverLine := ""
	if data, err := os.ReadFile(driverMarker()); err == nil {
		if added := strings.TrimSpace(string(data)); added != "" {
			driverLine = fmt.Sprintf(
				"Start-Sleep -Milliseconds 500; "+ // 스풀러가 드라이버 참조를 놓을 시간을 준다
					"Get-PrinterDriver -Name %s -ErrorAction SilentlyContinue | "+
					"Remove-PrinterDriver -ErrorAction SilentlyContinue",
				psQuote(added))
		}
	}

	// 순서 중요: 프린터 → 포트 → (우리가 설치했다면) 드라이버.
	// 드라이버는 다른 프린터가 쓰고 있으면 Remove 가 조용히 실패(SilentlyContinue)하여 보존된다.
	script := fmt.Sprintf(`
$name = %s
$port = %s
Get-Printer -Name $name -ErrorAction SilentlyContinue | Remove-Printer -ErrorAction SilentlyContinue
Get-PrinterPort -Name $port -ErrorAction SilentlyContinue | Remove-PrinterPort -ErrorAction SilentlyContinue
%s
'OK'
`, psQuote(PrinterName), psQuote(portName), driverLine)

	out, err := runPS(script)
	// 표식과 작업 폴더는 결과와 무관하게 정리한다.
	_ = os.RemoveAll(baseDir())
	if err != nil {
		return fmt.Errorf("프린터 제거 실패: %v\n%s", err, out)
	}
	return nil
}

// preferredDriver 는 가상 프린터에 쓸 인박스 드라이버 이름이다.
//
// "MS Publisher Color Printer"(인박스 컬러 PostScript 드라이버, pscript5 기반)를 쓰는 이유:
//   - 일반 프린터 드라이버라 ReportShop(ICT_REPORTX) 등이 받아들인다.
//     ("Microsoft Print To PDF" 같은 파일출력형 드라이버는 [지원불가]로 거부됨.)
//   - PrintCapabilities 에 Color 를 광고한다(PageOutputColor: Color) → Edge/Chrome 등
//     XPS 앱에서도 "색 → 컬러"를 고를 수 있다. (PS Class Driver 는 흑백만 광고했음.)
//   - PostScript 를 TCP 로 보내므로 리스너가 받아 gs 로 PDF 변환한다(컬러 보존).
//   - 모든 Windows 10/11 에 인박스로 제공(없으면 Add-PrinterDriver 로 스테이징).
//
// 남는 트레이드오프: PS→gs 변환이라 한글 등 텍스트 복사는 다소 깨질 수 있다(ToUnicode).
// 그건 Print To PDF 로만 완전 해결되는데 그 드라이버는 ReportShop 이 거부 → 호환을 우선.
const preferredDriver = "MS Publisher Color Printer"

// ensureDriver 는 설치에 사용할 드라이버를 결정한다.
// 우선순위: 환경변수 강제 지정 → 인박스 preferredDriver(필요시 자동 설치)
// → 시스템에 이미 있는 PostScript 드라이버 스캔(pickPSDriver).
func ensureDriver() (string, error) {
	if d := strings.TrimSpace(os.Getenv("EPRINTER_PRINTER_DRIVER")); d != "" {
		return d, nil
	}
	// 인박스 컬러 PostScript 드라이버(preferredDriver)를 확보한다.
	//  - 이미 있으면 'PRESENT'(우리가 안 건드림 → 종료 시 보존)
	//  - 우리가 새로 설치했으면 'ADDED'(종료 시 제거해 원복)
	//  - 못 구하면 'NO'(폴백)
	out, err := runPS(fmt.Sprintf(`
$d = %s
if (Get-PrinterDriver -Name $d -ErrorAction SilentlyContinue) { 'PRESENT' }
else {
  try {
    Add-PrinterDriver -Name $d -ErrorAction Stop
    if (Get-PrinterDriver -Name $d -ErrorAction SilentlyContinue) { 'ADDED' } else { 'NO' }
  } catch { 'NO' }
}
`, psQuote(preferredDriver)))
	switch {
	case err == nil && strings.Contains(out, "ADDED"):
		// 우리가 설치했음을 표식(어떤 드라이버인지 이름까지)으로 남겨 종료 시 제거한다.
		_ = os.MkdirAll(baseDir(), 0o755)
		_ = os.WriteFile(driverMarker(), []byte(preferredDriver), 0o644)
		return preferredDriver, nil
	case err == nil && strings.Contains(out, "PRESENT"):
		// 원래 있던 드라이버 → 표식 제거(보존 대상).
		_ = os.Remove(driverMarker())
		return preferredDriver, nil
	}
	// 폴백: 시스템에 이미 설치된 PostScript 드라이버를 찾는다(우리가 추가한 것 아님).
	_ = os.Remove(driverMarker())
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
	// 콘솔 창이 깜빡이지 않게 창 없이 실행한다.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	out, err := cmd.CombinedOutput()
	return string(out), err
}
