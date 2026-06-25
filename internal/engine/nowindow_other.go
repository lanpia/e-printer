//go:build !windows

package engine

import "os/exec"

// hideConsole 은 비 Windows 에서는 아무 동작도 하지 않는다.
func hideConsole(cmd *exec.Cmd) {}
