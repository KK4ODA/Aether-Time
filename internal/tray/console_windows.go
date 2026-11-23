//go:build windows

package tray

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	procFreeConsole           = kernel32.NewProc("FreeConsole")
)

// DetachOwnConsole closes the console window when the program was started by
// double-clicking it (the console then belongs to this process alone). When
// started from a terminal the console is shared and is left alone.
func DetachOwnConsole() {
	pids := make([]uint32, 4)
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if n == 1 {
		procFreeConsole.Call()
	}
}
