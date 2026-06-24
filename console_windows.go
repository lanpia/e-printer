//go:build windows

package main

import (
	"os"
	"syscall"
)

// windowsgui 서브시스템으로 빌드하면 콘솔이 없어 CLI 출력이 보이지 않는다.
// 콘솔(터미널)에서 인자와 함께 실행된 경우에 한해 부모 콘솔에 표준출력을 붙인다.
//
// 단, 이미 유효한 stdout 이 있으면(예: `eprinter info > out.txt` 처럼 리다이렉트되었거나
// 콘솔 핸들을 상속받은 경우) 그 핸들을 그대로 둔다 — 가로채면 리다이렉트가 깨진다.
func attachConsole() {
	if stdoutIsValid() {
		return
	}
	const attachParentProcess = ^uintptr(0) // (DWORD)-1
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	if r, _, _ := kernel32.NewProc("AttachConsole").Call(attachParentProcess); r == 0 {
		return // 부모 콘솔 없음(더블클릭 등)
	}
	if con, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout = con
		os.Stderr = con
	}
	if con, err := os.OpenFile("CONIN$", os.O_RDWR, 0); err == nil {
		os.Stdin = con
	}
}

// stdout 이 이미 연결돼 있는지(파일/파이프/콘솔) 확인한다.
func stdoutIsValid() bool {
	h, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil || h == 0 || h == syscall.InvalidHandle {
		return false
	}
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	// GetFileType: FILE_TYPE_UNKNOWN(0) 이면 미연결로 간주.
	ft, _, _ := kernel32.NewProc("GetFileType").Call(uintptr(h))
	return ft != 0
}
