//go:build !windows

package main

// 비 Windows 빌드용 no-op.
func attachConsole() {}
