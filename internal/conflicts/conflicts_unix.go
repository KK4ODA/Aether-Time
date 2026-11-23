//go:build linux || darwin

package conflicts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var linuxDaemons = map[string]string{
	"chronyd":           "chrony is disciplining the clock. Stop it (sudo systemctl disable --now chronyd) or run Aether Time in monitor mode.",
	"ntpd":              "ntpd is disciplining the clock. Stop it (sudo systemctl disable --now ntp) or run Aether Time in monitor mode.",
	"ntpsec":            "NTPsec is disciplining the clock. Stop it or run Aether Time in monitor mode.",
	"openntpd":          "OpenNTPD is disciplining the clock. Stop it or run Aether Time in monitor mode.",
	"systemd-timesyncd": "systemd-timesyncd will also set the clock. Disable it with: sudo timedatectl set-ntp false",
}

func detect() []Finding {
	if runtime.GOOS == "darwin" {
		return detectDarwin()
	}
	var out []Finding
	seen := map[string]bool{}
	dirs, _ := filepath.Glob("/proc/[0-9]*/comm")
	for _, p := range dirs {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(b))
		if advice, ok := linuxDaemons[name]; ok && !seen[name] {
			seen[name] = true
			out = append(out, Finding{Name: name, Advice: advice})
		}
	}
	return out
}

func detectDarwin() []Finding {
	// timed is macOS's built-in time daemon; "Set time and date automatically" controls it.
	out, err := exec.Command("/usr/sbin/systemsetup", "-getusingnetworktime").Output()
	if err == nil && strings.Contains(strings.ToLower(string(out)), ": on") {
		return []Finding{{Name: "timed (Set time automatically)",
			Advice: "macOS also sets the clock automatically. Turn off System Settings → General → Date & Time → Set time and date automatically, or use monitor mode."}}
	}
	return nil
}
