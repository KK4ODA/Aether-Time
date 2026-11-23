// SPDX-License-Identifier: GPL-3.0-or-later

package tray

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDescribe(t *testing.T) {
	var st apiStatus
	json.Unmarshal([]byte(`{"engine":{"state":"synced","mode":"auto","can_adjust":true,
		"offset_ms":-1.5,"error_ms":3.2,"used":["gps","ntp"]}}`), &st)
	v := describe(&st, false)
	if v.icon != StatusOK || v.headline != "Synchronized with GPS + NTP" {
		t.Fatalf("%+v", v)
	}
	if v.detail != "Clock error +1.50 ms ± 3.20 ms" || v.tooltip != "Aether Time: synchronized, +1.50 ms" {
		t.Fatalf("%q / %q", v.detail, v.tooltip)
	}
	st.Engine.CanAdjust = false
	if v := describe(&st, false); v.icon != StatusWarn || !strings.Contains(v.headline, "can't change") {
		t.Fatalf("%+v", v)
	}
	st.Engine.State = "holdover"
	if v := describe(&st, false); v.icon != StatusHold {
		t.Fatalf("%+v", v)
	}
	if v := describe(nil, false); v.icon != StatusUnknown || v.headline != "Aether Time is not running" {
		t.Fatalf("%+v", v)
	}
}

func TestClient(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.Header.Get("X-Aether") != "1" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		got = append(got, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/api/status" {
			w.Write([]byte(`{"engine":{"state":"tracking"},"update":{"available":true,"latest":{"version":"1.2.0"}}}`))
		}
	}))
	defer srv.Close()
	c := newClient(srv.URL)
	st, err := c.status()
	if err != nil || st.Engine.State != "tracking" || st.Update.Latest.Version != "1.2.0" {
		t.Fatalf("%+v %v", st, err)
	}
	if err := c.post("api/monitor", map[string]bool{"on": true}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "GET /api/status,POST /api/monitor" {
		t.Fatalf("%v", got)
	}
}

func TestAutostartFiles(t *testing.T) {
	d := desktopEntry("/opt/Aether Time/aether-time")
	if !strings.Contains(d, `Exec="/opt/Aether Time/aether-time" run --no-browser`) {
		t.Fatalf("desktop entry:\n%s", d)
	}
	p := launchAgent("/Applications/A&B/aether-time")
	if !strings.Contains(p, "<string>/Applications/A&amp;B/aether-time</string>") || !strings.Contains(p, "<true/>") {
		t.Fatalf("plist:\n%s", p)
	}
	if w := windowsCommand(`C:\Program Files\Aether Time\aether-time.exe`); w != `"C:\Program Files\Aether Time\aether-time.exe" run --no-browser` {
		t.Fatal(w)
	}
}

func TestSingleInstanceLock(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AppData", t.TempDir())
	release, ok := Lock()
	if !ok {
		t.Fatal("first lock failed")
	}
	if _, ok := Lock(); ok {
		t.Fatal("second lock should fail while the first is held")
	}
	release()
	release2, ok := Lock()
	if !ok {
		t.Fatal("lock not released")
	}
	release2()
}
