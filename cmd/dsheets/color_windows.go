//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// enableColor switches a Windows console to understand ANSI color codes, as
// Windows Terminal already does and older console windows need. It reports
// whether the console can show them.
func enableColor(f *os.File) bool {
	const enableVirtualTerminalProcessing = 0x0004
	kernel := syscall.NewLazyDLL("kernel32.dll")
	getMode, setMode := kernel.NewProc("GetConsoleMode"), kernel.NewProc("SetConsoleMode")
	var mode uint32
	if r, _, _ := getMode.Call(f.Fd(), uintptr(unsafe.Pointer(&mode))); r == 0 {
		return false
	}
	if mode&enableVirtualTerminalProcessing != 0 {
		return true
	}
	r, _, _ := setMode.Call(f.Fd(), uintptr(mode|enableVirtualTerminalProcessing))
	return r != 0
}
