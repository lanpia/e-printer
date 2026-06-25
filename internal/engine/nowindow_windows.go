//go:build windows

package engine

import (
	"os/exec"
	"syscall"
)

// hideConsole 은 자식 콘솔 프로그램(gswin64c.exe, gpcl6win64.exe, powershell 등)을
// 콘솔 창 없이 실행하게 한다. 이게 없으면 인쇄/변환 때마다 cmd 창이 깜빡인다.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}
