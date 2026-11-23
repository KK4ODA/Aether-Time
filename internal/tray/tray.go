// SPDX-License-Identifier: GPL-3.0-or-later

// Package tray shows Aether Time in the system tray (Windows notification
// area, macOS menu bar, Linux StatusNotifier panels). The tray talks to the
// engine through the local API, so it works both when the engine runs in the
// same process and when it runs as the system service.
package tray

import (
	"os/exec"
	"runtime"
)

// Options configure the tray.
type Options struct {
	// BaseURL of the dashboard, e.g. http://127.0.0.1:8123/
	BaseURL string
	// InProcess is true when this process also runs the engine.
	InProcess bool
	// OnQuit is called when the user chooses Quit.
	OnQuit func()
}

// OpenURL opens url in the default browser.
func OpenURL(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
