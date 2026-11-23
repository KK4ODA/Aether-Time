// Package svc installs and runs Aether Time as a system service: a Windows
// service, a systemd unit on Linux, or a launchd daemon on macOS. Running as a
// service gives it the right to set the clock and starts it at boot.
package svc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/kardianos/service"
)

// Name is the service name on every platform.
const Name = "aether-time"

func newService(prog service.Interface, configPath string) (service.Service, error) {
	cfg := &service.Config{
		Name:        Name,
		DisplayName: "Aether Time",
		Description: "Keeps the system clock exact using NTP, GPS and FT8/FT4 decode timing.",
		Arguments:   []string{"service", "run", "--config", configPath},
		Option: service.KeyValue{
			// Restart automatically, including after a self-update.
			"Restart":                "always",
			"OnFailure":              "restart",
			"OnFailureDelayDuration": "3s",
			"KeepAlive":              true,
			"RunAtLoad":              true,
		},
	}
	if prog == nil {
		prog = &program{}
	}
	return service.New(prog, cfg)
}

type program struct {
	run    func(ctx context.Context) error
	cancel context.CancelFunc
	done   chan struct{}
}

func (p *program) Start(service.Service) error {
	if p.run == nil {
		return errors.New("not runnable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	go func() {
		defer close(p.done)
		if err := p.run(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "aether-time:", err)
			os.Exit(1) // let the service manager restart us
		}
	}()
	return nil
}

func (p *program) Stop(service.Service) error {
	if p.cancel != nil {
		p.cancel()
		select {
		case <-p.done:
		case <-time.After(10 * time.Second):
		}
	}
	return nil
}

// Run runs fn under the service manager until it asks us to stop.
func Run(configPath string, fn func(ctx context.Context) error) error {
	s, err := newService(&program{run: fn}, configPath)
	if err != nil {
		return err
	}
	return s.Run()
}

// Control performs install, uninstall, start, stop or restart.
func Control(action, configPath string) error {
	s, err := newService(nil, configPath)
	if err != nil {
		return err
	}
	return service.Control(s, action)
}

// Status returns "running", "stopped", "not installed" or "unknown".
func Status() string {
	s, err := newService(nil, "")
	if err != nil {
		return "unknown"
	}
	st, err := s.Status()
	switch {
	case errors.Is(err, service.ErrNotInstalled):
		return "not installed"
	case err != nil:
		return "unknown"
	case st == service.StatusRunning:
		return "running"
	case st == service.StatusStopped:
		return "stopped"
	}
	return "unknown"
}

// Interactive reports whether we were started by a user rather than a service manager.
func Interactive() bool { return service.Interactive() }
