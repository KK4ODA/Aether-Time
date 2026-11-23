// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package conflicts

import (
	"golang.org/x/sys/windows"
)

func detect() []Finding {
	// SC_MANAGER_CONNECT + SERVICE_QUERY_STATUS work without administrator rights.
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return nil
	}
	defer windows.CloseServiceHandle(scm)
	var out []Finding
	for _, c := range []struct{ name, advice string }{
		{"W32Time", "The Windows Time service also sets the clock now and then. For steady FT8 timing set it to Manual (admin prompt): sc config w32time start= demand && net stop w32time"},
		{"NTP", "Meinberg NTP is running and will fight over the clock. Stop it or use Aether Time in monitor mode."},
		{"Dimension4", "Dimension 4 is running. Disable it or use Aether Time in monitor mode."},
	} {
		h, err := windows.OpenService(scm, windows.StringToUTF16Ptr(c.name), windows.SERVICE_QUERY_STATUS)
		if err != nil {
			continue
		}
		var st windows.SERVICE_STATUS
		err = windows.QueryServiceStatus(h, &st)
		windows.CloseServiceHandle(h)
		if err == nil && st.CurrentState == windows.SERVICE_RUNNING {
			out = append(out, Finding{Name: c.name, Advice: c.advice})
		}
	}
	return out
}
