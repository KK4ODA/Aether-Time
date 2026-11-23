// Command aether-time keeps the computer clock exact using internet time (NTP,
// HTTPS), GPS receivers and the timing of FT8/FT4 decodes, online or offline.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/KK4ODA/Aether-Time/internal/app"
	"github.com/KK4ODA/Aether-Time/internal/clock"
	"github.com/KK4ODA/Aether-Time/internal/config"
	"github.com/KK4ODA/Aether-Time/internal/fusion"
	"github.com/KK4ODA/Aether-Time/internal/ntp"
	"github.com/KK4ODA/Aether-Time/internal/svc"
	"github.com/KK4ODA/Aether-Time/internal/updater"
	"github.com/KK4ODA/Aether-Time/internal/version"
)

const usage = `Aether Time: keeps your clock exact with NTP, GPS and FT8/FT4 decode timing.

Usage:
  aether-time                     start and open the dashboard (same as "run")
  aether-time run [flags]         run in the foreground
      --config PATH               configuration file (default: %s)
      --monitor                   measure only, never change the clock
      --no-browser                do not open the dashboard
  aether-time sync [--dry-run]    one-shot sync from the configured NTP servers
  aether-time status              show the state of the running instance
  aether-time service install [--start] | uninstall | start | stop | restart | status
                                  run at boot with the right to set the clock
  aether-time update [--check]    check for / install the latest release
  aether-time config              print the configuration file path and contents
  aether-time version
`

func main() {
	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "run":
		err = cmdRun(args)
	case "service":
		err = cmdService(args)
	case "sync":
		err = cmdSync(args)
	case "status":
		err = cmdStatus(args)
	case "update":
		err = cmdUpdate(args)
	case "config":
		err = cmdConfig(args)
	case "version", "--version", "-v":
		fmt.Println(version.String())
	case "help", "--help", "-h":
		fmt.Printf(usage, config.ConfigPath())
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		fmt.Fprintf(os.Stderr, usage, config.ConfigPath())
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", config.ConfigPath(), "configuration file")
	monitor := fs.Bool("monitor", false, "measure only, never change the clock")
	noBrowser := fs.Bool("no-browser", false, "do not open the dashboard in a browser")
	fs.Parse(args)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	restarted := os.Getenv("AETHER_TIME_RESTARTED") == "1"
	openUI := cfg.Web.OpenBrowser && !*noBrowser && !restarted

	// If the service (or another copy) is already running, just show its dashboard.
	if url, ok := runningInstance(cfg.Web.Listen); ok {
		fmt.Println("Aether Time is already running:", url)
		if openUI {
			openBrowser(url)
		}
		return nil
	}

	a, err := app.New(app.Options{ConfigPath: *cfgPath, Monitor: *monitor, OpenBrowser: openUI})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = a.Run(ctx)
	if errors.Is(err, app.ErrRestart) {
		return reexec()
	}
	return err
}

// reexec replaces this process with the (updated) executable.
func reexec() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	env := append(os.Environ(), "AETHER_TIME_RESTARTED=1")
	if runtime.GOOS == "windows" {
		cmd := exec.Command(exe, os.Args[1:]...)
		cmd.Stdout, cmd.Stderr, cmd.Env = os.Stdout, os.Stderr, env
		if err := cmd.Start(); err != nil {
			return err
		}
		os.Exit(0)
	}
	return syscall.Exec(exe, os.Args, env)
}

func runningInstance(listen string) (string, bool) {
	host := strings.TrimPrefix(listen, "0.0.0.0")
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	url := "http://" + host + "/"
	c := http.Client{Timeout: 700 * time.Millisecond}
	resp, err := c.Get(url + "api/ping")
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	var v struct {
		App string `json:"app"`
	}
	if json.NewDecoder(resp.Body).Decode(&v) != nil || v.App != "aether-time" {
		return "", false
	}
	return url, true
}

func cmdService(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: aether-time service install [--start] | uninstall | start | stop | restart | status")
	}
	action := args[0]
	fs := flag.NewFlagSet("service", flag.ExitOnError)
	cfgPath := fs.String("config", config.ConfigPath(), "configuration file")
	start := fs.Bool("start", false, "start the service after installing it")
	fs.Parse(args[1:])

	switch action {
	case "run":
		return svc.Run(*cfgPath, func(ctx context.Context) error {
			a, err := app.New(app.Options{ConfigPath: *cfgPath, Service: true})
			if err != nil {
				return err
			}
			return a.Run(ctx) // ErrRestart makes the service manager restart us
		})
	case "status":
		fmt.Println(svc.Status())
		return nil
	case "install":
		// Make sure a configuration exists where the service will look for it.
		if _, err := os.Stat(*cfgPath); errors.Is(err, os.ErrNotExist) {
			if err := config.Save(*cfgPath, config.Default()); err != nil {
				return fmt.Errorf("writing %s: %w (run as administrator/root)", *cfgPath, err)
			}
		}
		if err := svc.Control("install", *cfgPath); err != nil {
			return fmt.Errorf("%w (run as administrator, or with sudo)", err)
		}
		fmt.Println("Service installed. Configuration:", *cfgPath)
		if *start {
			if err := svc.Control("start", *cfgPath); err != nil {
				return err
			}
			fmt.Println("Service started. Dashboard: http://127.0.0.1:8123/")
		}
		return nil
	case "uninstall", "start", "stop", "restart":
		if err := svc.Control(action, *cfgPath); err != nil {
			return fmt.Errorf("%w (run as administrator, or with sudo)", err)
		}
		fmt.Println("ok")
		return nil
	}
	return fmt.Errorf("unknown service action %q", action)
}

// cmdSync is a one-shot "ntpdate": query the configured servers, combine them
// and step the clock.
func cmdSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	cfgPath := fs.String("config", config.ConfigPath(), "configuration file")
	dry := fs.Bool("dry-run", false, "show the offset without changing the clock")
	fs.Parse(args)
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	servers := cfg.NTP.Servers
	if fs.NArg() > 0 {
		servers = fs.Args()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVER\tOFFSET\tDELAY\tSTRATUM\tSTATUS")
	var ests []fusion.Estimate
	for _, s := range servers {
		r, err := ntp.Query(ctx, s, 2*time.Second, time.Now)
		if err != nil {
			fmt.Fprintf(tw, "%s\t\t\t\t%v\n", s, err)
			continue
		}
		fmt.Fprintf(tw, "%s\t%+.1f ms\t%.1f ms\t%d\tok\n", s, msf(r.Offset), msf(r.Delay), r.Stratum)
		ests = append(ests, fusion.Estimate{ID: s, Offset: r.Offset, Error: r.ErrorBound()})
	}
	tw.Flush()
	res, ok := fusion.Combine(ests)
	if !ok {
		return errors.New("no server answered (offline? UDP port 123 blocked? the service also tries HTTPS)")
	}
	fmt.Printf("\nOffset %+.1f ms ± %.1f ms (%d of %d servers agree)\n", msf(res.Offset), msf(res.Error), len(res.Used), len(ests))
	if *dry {
		return nil
	}
	clk := clock.System{}
	if ok, why := clk.CanAdjust(); !ok {
		return errors.New("cannot set the clock: " + why)
	}
	if err := clk.Step(res.Offset); err != nil {
		return err
	}
	fmt.Println("Clock corrected.")
	return nil
}

func msf(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	cfgPath := fs.String("config", config.ConfigPath(), "configuration file")
	fs.Parse(args)
	cfg, _ := config.Load(*cfgPath)
	url, ok := runningInstance(cfg.Web.Listen)
	if !ok {
		fmt.Println("Aether Time is not running (service:", svc.Status()+")")
		return nil
	}
	resp, err := http.Get(url + "api/status")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var st struct {
		Engine struct {
			State    string   `json:"state"`
			Note     string   `json:"note"`
			OffsetMs *float64 `json:"offset_ms"`
			ErrorMs  *float64 `json:"error_ms"`
			Used     []string `json:"used"`
			DriftPPM *float64 `json:"drift_ppm"`
			Sources  []struct {
				ID       string   `json:"id"`
				State    string   `json:"state"`
				Message  string   `json:"message"`
				OffsetMs *float64 `json:"offset_ms"`
				Used     bool     `json:"used"`
			} `json:"sources"`
		} `json:"engine"`
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return err
	}
	e := st.Engine
	fmt.Printf("Aether Time %s at %s\nState: %s", st.Info.Version, url, e.State)
	if e.OffsetMs != nil {
		fmt.Printf("   offset %+.1f ms ± %.1f ms   from %s", *e.OffsetMs, *e.ErrorMs, strings.Join(e.Used, ", "))
	}
	if e.DriftPPM != nil {
		fmt.Printf("   drift %+.2f ppm", *e.DriftPPM)
	}
	fmt.Println()
	if e.Note != "" {
		fmt.Println("Note:", e.Note)
	}
	sort.Slice(e.Sources, func(i, j int) bool { return e.Sources[i].ID < e.Sources[j].ID })
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "\nSOURCE\tSTATE\tOFFSET\tUSED\tDETAIL")
	for _, s := range e.Sources {
		off := ""
		if s.OffsetMs != nil {
			off = fmt.Sprintf("%+.1f ms", *s.OffsetMs)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%v\t%s\n", s.ID, s.State, off, s.Used, s.Message)
	}
	return tw.Flush()
}

func cmdUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	cfgPath := fs.String("config", config.ConfigPath(), "configuration file")
	checkOnly := fs.Bool("check", false, "only check, do not install")
	fs.Parse(args)
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	cfg.Update.Enabled = true
	u := updater.New(cfg.Update, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	rel, err := u.Check(ctx)
	if err != nil {
		return err
	}
	if rel == nil {
		fmt.Println("Aether Time", version.Version, "is up to date.")
		return nil
	}
	fmt.Printf("Update available: %s → %s\n%s\n", version.Version, rel.Version, rel.URL)
	if *checkOnly {
		return nil
	}
	if !u.Status().Signed {
		fmt.Println("Note: this build has no signing key; only the SHA-256 checksum is verified.")
	}
	if err := u.Install(ctx); err != nil {
		return err
	}
	fmt.Println("Installed", rel.Version+".")
	if svc.Status() == "running" {
		if err := svc.Control("restart", *cfgPath); err == nil {
			fmt.Println("Service restarted.")
		}
	}
	return nil
}

func cmdConfig(args []string) error {
	fs := flag.NewFlagSet("config", flag.ExitOnError)
	cfgPath := fs.String("config", config.ConfigPath(), "configuration file")
	fs.Parse(args)
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	fmt.Println("#", *cfgPath)
	b, _ := json.MarshalIndent(cfg, "", "  ")
	fmt.Println(string(b))
	return nil
}

func openBrowser(url string) {
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
