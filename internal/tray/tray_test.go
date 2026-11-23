// SPDX-License-Identifier: GPL-3.0-or-later

package tray

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIconBadgeColours(t *testing.T) {
	for st, want := range colBadge {
		img, err := png.Decode(bytes.NewReader(PNG(32, st)))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != 32 {
			t.Fatalf("size %v", img.Bounds())
		}
		// The centre of the badge carries the status colour.
		r, g, b, a := img.At(24, 24).RGBA()
		if uint8(r>>8) != want.R || uint8(g>>8) != want.G || uint8(b>>8) != want.B || a>>8 != 0xFF {
			t.Fatalf("status %d: badge colour %02x%02x%02x", st, r>>8, g>>8, b>>8)
		}
		// Corners are transparent, the dial is opaque.
		if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
			t.Fatal("corner not transparent")
		}
	}
}

func TestICOContainer(t *testing.T) {
	ico := ICO(StatusOK, 16, 32)
	var hdr [3]uint16
	binary.Read(bytes.NewReader(ico), binary.LittleEndian, &hdr)
	if hdr != [3]uint16{0, 1, 2} {
		t.Fatalf("header %v", hdr)
	}
	for i, size := range []int{16, 32} {
		e := ico[6+16*i:]
		if int(e[0]) != size || int(e[1]) != size {
			t.Fatalf("entry %d size %d", i, e[0])
		}
		n := binary.LittleEndian.Uint32(e[8:])
		off := binary.LittleEndian.Uint32(e[12:])
		img, err := png.Decode(bytes.NewReader(ico[off : off+n]))
		if err != nil || img.Bounds().Dx() != size {
			t.Fatalf("frame %d: %v", i, err)
		}
	}
}

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
