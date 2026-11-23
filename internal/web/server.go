// SPDX-License-Identifier: GPL-3.0-or-later

// Package web serves the dashboard and the JSON API.
package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KK4ODA/Aether-Time/internal/config"
	"github.com/KK4ODA/Aether-Time/internal/conflicts"
	"github.com/KK4ODA/Aether-Time/internal/engine"
	"github.com/KK4ODA/Aether-Time/internal/logbuf"
	"github.com/KK4ODA/Aether-Time/internal/updater"
)

//go:embed static
var staticFS embed.FS

// Info describes the installation.
type Info struct {
	Version       string   `json:"version"`
	Commit        string   `json:"commit"`
	OS            string   `json:"os"`
	Arch          string   `json:"arch"`
	ConfigPath    string   `json:"config_path"`
	DataDir       string   `json:"data_dir"`
	Service       bool     `json:"service"`
	ServiceStatus string   `json:"service_status"`
	CanElevate    bool     `json:"can_elevate"`
	SerialPorts   []string `json:"serial_ports"`
	AllTxt        []string `json:"all_txt_candidates"`
}

// Backend is what the web layer needs from the application.
type Backend interface {
	Snapshot() engine.Snapshot
	History(since int64) ([]engine.Point, []engine.Correction)
	Config() config.Config
	ApplyConfig(config.Config) (restartNeeded bool, err error)
	SyncNow()
	ManualAdjust(time.Duration) error
	SetMonitor(bool)
	UpdateStatus() updater.Status
	CheckUpdate(context.Context) error
	InstallUpdate(context.Context) error
	Logs() []logbuf.Entry
	Conflicts() []conflicts.Finding
	SendGrid() (grid string, sent int, err error)
	ForgetLearned(key string)
	InstallService() error
	Info() Info
}

// Server is the HTTP server.
type Server struct {
	b   Backend
	log *slog.Logger

	mu    sync.Mutex
	addr  string
	ready chan struct{}
	cfg   config.WebConfig
}

// New creates a server.
func New(b Backend, log *slog.Logger) *Server {
	return &Server{b: b, log: log, ready: make(chan struct{})}
}

// URL returns the dashboard address once listening.
func (s *Server) URL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	host, port, _ := net.SplitHostPort(s.addr)
	if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() || ip.IsLoopback() {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/"
}

// WaitReady waits until the server listens.
func (s *Server) WaitReady(d time.Duration) bool {
	select {
	case <-s.ready:
		return true
	case <-time.After(d):
		return false
	}
}

// ListenAndServe serves until ctx is done. Binding is retried for a while,
// because after an update or a hand-over the previous process may still hold the port.
func (s *Server) ListenAndServe(ctx context.Context, cfg config.WebConfig) error {
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	var ln net.Listener
	var err error
	for attempt := 0; ; attempt++ {
		ln, err = net.Listen("tcp", cfg.Listen)
		if err == nil {
			break
		}
		if attempt == 0 {
			s.log.Warn("dashboard port busy; retrying", "addr", cfg.Listen, "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
		if attempt > 30 {
			return err
		}
	}
	s.mu.Lock()
	s.addr = ln.Addr().String()
	s.mu.Unlock()
	close(s.ready)
	s.log.Info("dashboard ready", "url", s.URL())

	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Handler returns the HTTP handler with security checks applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	files := http.FileServer(http.FS(static))
	mux.Handle("GET /", files)
	mux.HandleFunc("GET /clock", func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/clock.html"
		files.ServeHTTP(w, r2)
	})

	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"app": "aether-time", "version": s.b.Info().Version})
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.status()) })
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("GET /api/history", func(w http.ResponseWriter, r *http.Request) {
		minutes, _ := strconv.Atoi(r.URL.Query().Get("minutes"))
		if minutes <= 0 || minutes > 24*60 {
			minutes = 60
		}
		pts, cs := s.b.History(time.Now().Add(-time.Duration(minutes) * time.Minute).UnixMilli())
		if pts == nil {
			pts = []engine.Point{}
		}
		if cs == nil {
			cs = []engine.Correction{}
		}
		writeJSON(w, map[string]any{"points": pts, "corrections": cs})
	})
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.b.Config()
		if cfg.Web.Password != "" {
			cfg.Web.Password = passwordMask
		}
		writeJSON(w, cfg)
	})
	mux.HandleFunc("PUT /api/config", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.b.Config()
		oldPw := cfg.Web.Password
		dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
		if err := dec.Decode(&cfg); err != nil {
			httpError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		if cfg.Web.Password == passwordMask {
			cfg.Web.Password = oldPw
		}
		restart, err := s.b.ApplyConfig(cfg)
		if err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "restart_needed": restart})
	})
	mux.HandleFunc("POST /api/sync", func(w http.ResponseWriter, r *http.Request) {
		s.b.SyncNow()
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/adjust", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Ms float64 `json:"ms"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil || req.Ms == 0 {
			httpError(w, http.StatusBadRequest, "body must be {\"ms\": <non-zero milliseconds>}")
			return
		}
		if err := s.b.ManualAdjust(time.Duration(req.Ms * float64(time.Millisecond))); err != nil {
			httpError(w, http.StatusForbidden, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/monitor", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			On bool `json:"on"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
		s.b.SetMonitor(req.On)
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/update/check", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := s.b.CheckUpdate(ctx); err != nil {
			httpError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, s.b.UpdateStatus())
	})
	mux.HandleFunc("POST /api/update/install", func(w http.ResponseWriter, r *http.Request) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			if err := s.b.InstallUpdate(ctx); err != nil {
				s.log.Warn("update failed", "err", err)
			}
		}()
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/grid/send", func(w http.ResponseWriter, r *http.Request) {
		grid, n, err := s.b.SendGrid()
		if err != nil {
			httpError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "grid": grid, "sent": n})
	})
	mux.HandleFunc("POST /api/learned/forget", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Key string `json:"key"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil || req.Key == "" {
			httpError(w, http.StatusBadRequest, "body must be {\"key\": \"...\"}")
			return
		}
		s.b.ForgetLearned(req.Key)
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/service/install", func(w http.ResponseWriter, r *http.Request) {
		if err := s.b.InstallService(); err != nil {
			httpError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/logs", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.b.Logs()) })
	return s.secure(mux)
}

const passwordMask = "********"

func (s *Server) status() map[string]any {
	return map[string]any{
		"engine":    s.b.Snapshot(),
		"update":    s.b.UpdateStatus(),
		"info":      s.b.Info(),
		"conflicts": s.b.Conflicts(),
	}
}

// events streams the status once a second (Server-Sent Events).
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		b, err := json.Marshal(s.status())
		if err != nil {
			return
		}
		if _, err := w.Write([]byte("data: ")); err != nil {
			return
		}
		w.Write(b)
		w.Write([]byte("\n\n"))
		fl.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
		}
	}
}

// secure applies the access rules:
//   - When bound to loopback, only Host headers naming the loopback interface
//     are accepted (blocks DNS-rebinding attacks from web pages).
//   - Requests that change something must carry the X-Aether header and, if
//     the browser sends an Origin, it must be this server. Other web sites
//     cannot add custom headers without a CORS preflight, which we never grant.
//   - A password (HTTP Basic auth) is required when listening on the network,
//     except for programs on this computer that address it as localhost.
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		cfg := s.cfg
		s.mu.Unlock()
		loopback := config.IsLoopbackListen(cfg.Listen) || cfg.Listen == ""
		if loopback && !loopbackHost(r.Host) {
			httpError(w, http.StatusForbidden, "forbidden host")
			return
		}
		// Programs on this computer (the tray icon, the CLI) don't need the
		// password. Requiring a loopback Host as well keeps DNS-rebinding web
		// pages out.
		local := isLoopbackRemote(r.RemoteAddr) && loopbackHost(r.Host)
		if cfg.Password != "" && !local {
			_, pw, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(pw), []byte(cfg.Password)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="Aether Time"`)
				httpError(w, http.StatusUnauthorized, "password required")
				return
			}
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("X-Aether") != "1" {
				httpError(w, http.StatusForbidden, "missing X-Aether header")
				return
			}
			if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				if err != nil || u.Host != r.Host {
					httpError(w, http.StatusForbidden, "cross-origin request refused")
					return
				}
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLoopbackRemote(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
