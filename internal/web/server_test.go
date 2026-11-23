// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KK4ODA/Aether-Time/internal/config"
	"github.com/KK4ODA/Aether-Time/internal/conflicts"
	"github.com/KK4ODA/Aether-Time/internal/engine"
	"github.com/KK4ODA/Aether-Time/internal/logbuf"
	"github.com/KK4ODA/Aether-Time/internal/updater"
)

type fakeBackend struct {
	cfg    config.Config
	synced int
}

func (f *fakeBackend) Snapshot() engine.Snapshot { return engine.Snapshot{State: "synced"} }
func (f *fakeBackend) History(int64) ([]engine.Point, []engine.Correction) {
	return nil, nil
}
func (f *fakeBackend) Config() config.Config { return f.cfg }
func (f *fakeBackend) ApplyConfig(c config.Config) (bool, error) {
	if err := c.Validate(); err != nil {
		return false, err
	}
	f.cfg = c
	return false, nil
}
func (f *fakeBackend) SyncNow()                            { f.synced++ }
func (f *fakeBackend) ManualAdjust(time.Duration) error    { return nil }
func (f *fakeBackend) SetMonitor(bool)                     {}
func (f *fakeBackend) UpdateStatus() updater.Status        { return updater.Status{} }
func (f *fakeBackend) CheckUpdate(context.Context) error   { return nil }
func (f *fakeBackend) InstallUpdate(context.Context) error { return nil }
func (f *fakeBackend) Logs() []logbuf.Entry                { return nil }
func (f *fakeBackend) Conflicts() []conflicts.Finding      { return nil }
func (f *fakeBackend) SendGrid() (string, int, error)      { return "", 0, nil }
func (f *fakeBackend) ForgetLearned(string)                {}
func (f *fakeBackend) InstallService() error               { return nil }
func (f *fakeBackend) Info() Info                          { return Info{Version: "test"} }

func newTestServer(listen, password string) (*Server, *fakeBackend) {
	b := &fakeBackend{cfg: config.Default()}
	b.cfg.Web.Listen, b.cfg.Web.Password = listen, password
	s := New(b, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.cfg = b.cfg.Web
	return s, b
}

func do(h http.Handler, method, path, host string, hdr map[string]string, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = host
	r.RemoteAddr = "127.0.0.1:50000"
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestLoopbackProtections(t *testing.T) {
	s, b := newTestServer("127.0.0.1:8123", "")
	h := s.Handler()
	if w := do(h, "GET", "/api/ping", "127.0.0.1:8123", nil, ""); w.Code != 200 {
		t.Fatalf("ping: %d", w.Code)
	}
	if w := do(h, "GET", "/", "localhost:8123", nil, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Aether Time") {
		t.Fatalf("index: %d", w.Code)
	}
	if w := do(h, "GET", "/api/status", "attacker.example:8123", nil, ""); w.Code != 403 {
		t.Fatalf("DNS rebinding host accepted: %d", w.Code)
	}
	if w := do(h, "POST", "/api/sync", "127.0.0.1:8123", nil, ""); w.Code != 403 {
		t.Fatalf("POST without X-Aether accepted: %d", w.Code)
	}
	if w := do(h, "POST", "/api/sync", "127.0.0.1:8123", map[string]string{"X-Aether": "1", "Origin": "https://evil.example"}, ""); w.Code != 403 {
		t.Fatalf("cross-origin POST accepted: %d", w.Code)
	}
	if w := do(h, "POST", "/api/sync", "127.0.0.1:8123", map[string]string{"X-Aether": "1", "Origin": "http://127.0.0.1:8123"}, ""); w.Code != 200 || b.synced != 1 {
		t.Fatalf("same-origin POST: %d synced=%d", w.Code, b.synced)
	}
}

func TestPasswordOnNetwork(t *testing.T) {
	s, b := newTestServer("0.0.0.0:8123", "s3cret")
	h := s.Handler()
	if w := do(h, "GET", "/api/status", "shack-pi.local:8123", nil, ""); w.Code != 401 {
		t.Fatalf("no auth: %d", w.Code)
	}
	r := httptest.NewRequest("GET", "/api/config", nil)
	r.Host = "shack-pi.local:8123"
	r.RemoteAddr = "192.168.1.20:50000"
	r.SetBasicAuth("admin", "s3cret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), "s3cret") {
		t.Fatalf("config with auth: %d %s", w.Code, w.Body.String())
	}
	// Saving the masked password keeps the real one.
	r = httptest.NewRequest("PUT", "/api/config", strings.NewReader(w.Body.String()))
	r.Host = "shack-pi.local:8123"
	r.RemoteAddr = "192.168.1.20:50000"
	r.Header.Set("X-Aether", "1")
	r.SetBasicAuth("admin", "s3cret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || b.cfg.Web.Password != "s3cret" {
		t.Fatalf("save: %d %s password=%q", w.Code, w.Body.String(), b.cfg.Web.Password)
	}
}

func TestInvalidConfigRejected(t *testing.T) {
	s, _ := newTestServer("127.0.0.1:8123", "")
	w := do(s.Handler(), "PUT", "/api/config", "127.0.0.1:8123", map[string]string{"X-Aether": "1"}, `{"mode":"sometimes"}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "mode") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

// The tray and CLI on the same computer don't need the network password, but
// a DNS-rebinding page (loopback address, foreign Host) still does.
func TestLocalClientsSkipPassword(t *testing.T) {
	s, _ := newTestServer("0.0.0.0:8123", "s3cret")
	h := s.Handler()
	if w := do(h, "GET", "/api/status", "127.0.0.1:8123", nil, ""); w.Code != 200 {
		t.Fatalf("local client: %d", w.Code)
	}
	if w := do(h, "GET", "/api/status", "rebind.attacker.example:8123", nil, ""); w.Code != 401 {
		t.Fatalf("rebinding request: %d", w.Code)
	}
}
