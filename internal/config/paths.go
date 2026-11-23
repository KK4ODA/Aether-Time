package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// DataDir returns the directory holding config.json, state.json and logs.
//
// When running with administrator/root rights (which is required to set the
// clock, and is how the system service runs) a machine-wide directory is used.
// Otherwise a per-user directory is used so monitor-only mode still works.
//
// The AETHER_TIME_HOME environment variable overrides the choice.
func DataDir() string {
	if d := os.Getenv("AETHER_TIME_HOME"); d != "" {
		return d
	}
	if sys := systemDir(); sys != "" && dirUsable(sys) {
		return sys
	}
	if base, err := os.UserConfigDir(); err == nil {
		return filepath.Join(base, "AetherTime")
	}
	return "."
}

func systemDir() string {
	switch runtime.GOOS {
	case "windows":
		pd := os.Getenv("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		return filepath.Join(pd, "AetherTime")
	case "darwin":
		return "/Library/Application Support/AetherTime"
	default:
		return "/var/lib/aether-time"
	}
}

// dirUsable reports whether dir exists and is writable, or can be created.
func dirUsable(dir string) bool {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	f, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

// ConfigPath returns the default configuration file path.
func ConfigPath() string { return filepath.Join(DataDir(), "config.json") }
