// SPDX-License-Identifier: GPL-3.0-or-later

// Package app wires configuration, logging, the engine, the updater and the
// web dashboard together.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/KK4ODA/Aether-Time/internal/clock"
	"github.com/KK4ODA/Aether-Time/internal/config"
	"github.com/KK4ODA/Aether-Time/internal/conflicts"
	"github.com/KK4ODA/Aether-Time/internal/engine"
	"github.com/KK4ODA/Aether-Time/internal/logbuf"
	"github.com/KK4ODA/Aether-Time/internal/sources"
	"github.com/KK4ODA/Aether-Time/internal/svc"
	"github.com/KK4ODA/Aether-Time/internal/tray"
	"github.com/KK4ODA/Aether-Time/internal/updater"
	"github.com/KK4ODA/Aether-Time/internal/version"
	"github.com/KK4ODA/Aether-Time/internal/web"
)

// ErrRestart is returned by Run when the program should be restarted (after an update).
var ErrRestart = errors.New("restart requested")

// Options control how the app runs.
type Options struct {
	ConfigPath  string
	Monitor     bool // never touch the clock, regardless of config
	Service     bool // running under the service manager
	OpenBrowser bool
	Clock       clock.Clock
	LogOutput   io.Writer
}

// App is the running program.
type App struct {
	opts    Options
	dataDir string
	log     *slog.Logger
	logs    *logbuf.Buffer
	level   *slog.LevelVar
	state   *engine.State
	hist    *engine.History
	clk     clock.Clock
	upd     *updater.Updater

	mu        sync.Mutex
	cfg       config.Config
	eng       *engine.Engine
	engStop   context.CancelFunc
	engDone   chan struct{}
	rootCtx   context.Context
	conflicts []conflicts.Finding
	shutdown  context.CancelFunc
	restart   bool
	startedAt time.Time
}

// New loads the configuration and prepares the app.
func New(opts Options) (*App, error) {
	if opts.ConfigPath == "" {
		opts.ConfigPath = config.ConfigPath()
	}
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("configuration %s: %w", opts.ConfigPath, err)
	}
	dataDir := filepath.Dir(opts.ConfigPath)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	// Write a config file on first run so users can find and edit it.
	if _, err := os.Stat(opts.ConfigPath); errors.Is(err, os.ErrNotExist) {
		_ = config.Save(opts.ConfigPath, cfg)
	}

	a := &App{opts: opts, dataDir: dataDir, cfg: cfg, logs: logbuf.New(500), level: new(slog.LevelVar), startedAt: time.Now()}
	a.setLevel(cfg)
	out := opts.LogOutput
	if out == nil {
		out = os.Stderr
	}
	if f := openLogFile(filepath.Join(dataDir, "aether-time.log")); f != nil {
		out = io.MultiWriter(out, f)
	}
	a.log = slog.New(logbuf.NewHandler(slog.NewTextHandler(out, &slog.HandlerOptions{Level: a.level}), a.logs, a.level))
	a.clk = opts.Clock
	if a.clk == nil {
		a.clk = clock.System{}
	}
	a.state = engine.LoadState(filepath.Join(dataDir, "state.json"))
	a.hist = engine.NewHistory()
	a.upd = updater.New(cfg.Update, a.log)
	a.upd.OnInstalled = a.Restart
	return a, nil
}

func (a *App) setLevel(cfg config.Config) {
	if cfg.Log.Debug {
		a.level.Set(slog.LevelDebug)
	} else {
		a.level.Set(slog.LevelInfo)
	}
}

// openLogFile opens the log file, rotating it when it grows beyond 5 MB.
func openLogFile(path string) *os.File {
	if fi, err := os.Stat(path); err == nil && fi.Size() > 5<<20 {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	return f
}

// Logger returns the app logger.
func (a *App) Logger() *slog.Logger { return a.log }

// Run runs until ctx is cancelled or a restart is requested.
func (a *App) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	a.mu.Lock()
	a.shutdown = cancel
	a.rootCtx = ctx
	a.mu.Unlock()

	a.log.Info("starting", "version", version.String(), "config", a.opts.ConfigPath)
	if a.opts.Service {
		noteInstalledVersion()
	}
	if ok, why := a.clk.CanAdjust(); !ok {
		a.log.Warn("cannot set the clock; running in monitor mode", "how_to_fix", why)
	}
	a.startEngine()
	go a.upd.Run(ctx)
	go a.watchConflicts(ctx)

	srv := web.New(a, a.log)
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe(ctx, a.Config().Web) }()
	if a.opts.OpenBrowser {
		go func() {
			if srv.WaitReady(10 * time.Second) {
				tray.OpenURL(srv.URL())
			}
		}()
	}

	var err error
	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	cancel()
	a.stopEngine()
	_ = a.state.Save()
	a.mu.Lock()
	restart := a.restart
	a.mu.Unlock()
	if restart {
		return ErrRestart
	}
	return err
}

func (a *App) watchConflicts(ctx context.Context) {
	for {
		found := conflicts.Detect()
		a.mu.Lock()
		a.conflicts = found
		a.mu.Unlock()
		for _, f := range found {
			a.log.Warn("another time program is active", "program", f.Name)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Minute):
		}
	}
}

func (a *App) startEngine() {
	a.mu.Lock()
	defer a.mu.Unlock()
	cfg := a.cfg
	csv := ""
	if cfg.Log.SyncLog {
		csv = filepath.Join(a.dataDir, "sync-log.csv")
	}
	e := engine.New(cfg, engine.Deps{Clock: a.clk, State: a.state, History: a.hist, Logger: a.log, SyncLogPath: csv})
	if a.opts.Monitor {
		e.SetMonitor(true)
	}
	ctx, cancel := context.WithCancel(a.rootCtx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.Run(ctx)
	}()
	a.eng, a.engStop, a.engDone = e, cancel, done
}

func (a *App) stopEngine() {
	a.mu.Lock()
	stop, done := a.engStop, a.engDone
	a.mu.Unlock()
	if stop != nil {
		stop()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			a.log.Warn("engine did not stop in time")
		}
	}
}

// Restart asks Run to return ErrRestart so the caller can re-execute the program.
func (a *App) Restart() {
	a.mu.Lock()
	a.restart = true
	cancel := a.shutdown
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *App) engine() *engine.Engine {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.eng
}

// ---- web.Backend ----

// Snapshot implements web.Backend.
func (a *App) Snapshot() engine.Snapshot { return a.engine().Snapshot() }

// History implements web.Backend.
func (a *App) History(since int64) ([]engine.Point, []engine.Correction) {
	return a.engine().History(since)
}

// Config implements web.Backend.
func (a *App) Config() config.Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

// ApplyConfig validates, saves and applies a new configuration. Sources are
// restarted; learned values and history are kept.
func (a *App) ApplyConfig(cfg config.Config) (restartNeeded bool, err error) {
	if err := cfg.Validate(); err != nil {
		return false, err
	}
	if err := config.Save(a.opts.ConfigPath, cfg); err != nil {
		return false, err
	}
	a.mu.Lock()
	old := a.cfg
	a.cfg = cfg
	a.mu.Unlock()
	a.setLevel(cfg)
	a.stopEngine()
	a.startEngine()
	if old.Update != cfg.Update {
		a.log.Info("update settings changed; they take effect after a restart")
		restartNeeded = true
	}
	if old.Web != cfg.Web {
		restartNeeded = true
	}
	a.log.Info("configuration applied")
	return restartNeeded, nil
}

// SyncNow implements web.Backend.
func (a *App) SyncNow() { a.engine().SyncNow() }

// ManualAdjust implements web.Backend.
func (a *App) ManualAdjust(d time.Duration) error { return a.engine().ManualAdjust(d) }

// SetMonitor implements web.Backend.
func (a *App) SetMonitor(on bool) { a.engine().SetMonitor(on) }

// UpdateStatus implements web.Backend.
func (a *App) UpdateStatus() updater.Status { return a.upd.Status() }

// CheckUpdate implements web.Backend.
func (a *App) CheckUpdate(ctx context.Context) error {
	_, err := a.upd.Check(ctx)
	return err
}

// InstallUpdate implements web.Backend.
func (a *App) InstallUpdate(ctx context.Context) error { return a.upd.Install(ctx) }

// Logs implements web.Backend.
func (a *App) Logs() []logbuf.Entry { return a.logs.Entries() }

// Conflicts implements web.Backend.
func (a *App) Conflicts() []conflicts.Finding {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]conflicts.Finding(nil), a.conflicts...)
}

// SendGrid implements web.Backend.
func (a *App) SendGrid() (string, int, error) {
	e := a.engine()
	pos, ok := e.Position()
	if !ok || pos.Grid == "" {
		return "", 0, errors.New("no GPS position yet")
	}
	w, ok := e.Source("wsjtx").(*sources.WSJTX)
	if !ok {
		return pos.Grid, 0, errors.New("the WSJT-X source is disabled")
	}
	return pos.Grid, w.SendGrid(pos.Grid), nil
}

// ForgetLearned implements web.Backend.
func (a *App) ForgetLearned(key string) {
	a.state.Delete(key)
	_ = a.state.Save()
	a.stopEngine()
	a.startEngine()
}

// InstallService implements web.Backend (Windows: UAC prompt).
func (a *App) InstallService() error {
	if !svc.CanElevate() {
		return svc.RunElevated()
	}
	if err := svc.RunElevated("service", "install", "--start", "--config", a.opts.ConfigPath); err != nil {
		return err
	}
	// Hand over to the service once it runs: it needs our web port.
	go func() {
		for i := 0; i < 60; i++ {
			time.Sleep(2 * time.Second)
			if svc.Status() == "running" && !a.opts.Service {
				a.log.Info("the Aether Time service is running; handing over to it")
				// Restarting makes this program come back as a tray icon for the service.
				a.mu.Lock()
				a.restart = true
				cancel := a.shutdown
				a.mu.Unlock()
				if cancel != nil {
					cancel()
				}
				return
			}
		}
	}()
	return nil
}

// Info implements web.Backend.
func (a *App) Info() web.Info {
	return web.Info{
		Version:       version.Version,
		Commit:        version.Commit,
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		ConfigPath:    a.opts.ConfigPath,
		DataDir:       a.dataDir,
		Service:       a.opts.Service,
		ServiceStatus: svc.Status(),
		CanElevate:    svc.CanElevate(),
		SerialPorts:   sources.SerialPorts(),
		AllTxt:        sources.AllTxtCandidates(),
	}
}
