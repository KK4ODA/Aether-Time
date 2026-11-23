//go:build !windows

package tray

// DetachOwnConsole is only needed on Windows.
func DetachOwnConsole() {}
