// SPDX-License-Identifier: GPL-3.0-or-later

package tray

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The login item starts the normal program without opening the browser. If
// the service is running it only shows the tray; otherwise it also runs the
// engine.
var autostartArgs = []string{"run", "--no-browser"}

func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe, nil
}

// desktopEntry is the XDG autostart file used on Linux desktops.
func desktopEntry(exe string) string {
	quote := func(s string) string {
		if strings.ContainsAny(s, " \t\"\\$`") {
			r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`")
			return `"` + r.Replace(s) + `"`
		}
		return s
	}
	parts := []string{quote(exe)}
	for _, a := range autostartArgs {
		parts = append(parts, quote(a))
	}
	return fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Aether Time
Comment=Keeps the clock exact for FT8 and other digital modes
Exec=%s
Terminal=false
X-GNOME-Autostart-enabled=true
`, strings.Join(parts, " "))
}

// launchAgent is the macOS login item.
func launchAgent(exe string) string {
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	args := "\t\t<string>" + esc.Replace(exe) + "</string>\n"
	for _, a := range autostartArgs {
		args += "\t\t<string>" + a + "</string>\n"
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + launchAgentLabel + `</string>
	<key>ProgramArguments</key>
	<array>
` + args + `	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>ProcessType</key>
	<string>Interactive</string>
</dict>
</plist>
`
}

const launchAgentLabel = "io.github.kk4oda.aether-time.tray"

// windowsCommand is the value stored under the Run registry key.
func windowsCommand(exe string) string {
	return `"` + exe + `" ` + strings.Join(autostartArgs, " ")
}
