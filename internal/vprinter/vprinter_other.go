//go:build !windows

package vprinter

import "errors"

const PrinterName = "eprinter"

var errUnsupported = errors.New("가상 프린터 기능은 Windows 에서만 지원됩니다")

func IsAdmin() bool                       { return false }
func RunElevated(string, ...string) error { return errUnsupported }
func Installed() bool                      { return false }
func Install() error                       { return errUnsupported }
func Remove() error                        { return errUnsupported }
