//go:build windows || (linux && !android) || (darwin && cgo)

package tray

import (
	"time"

	"fyne.io/systray"
)

// Supported reports whether a tray icon can be shown in this session.
func Supported() bool { return platformSupported() }

// Run shows the tray icon and blocks until Quit. It must be called from the
// main goroutine (a macOS requirement).
func Run(opts Options) {
	systray.Run(func() { onReady(opts) }, nil)
}

// Quit removes the tray icon and makes Run return.
func Quit() { systray.Quit() }

func onReady(opts Options) {
	c := newClient(opts.BaseURL)
	icon := StatusUnknown
	systray.SetIcon(iconBytes(icon))
	systray.SetTooltip("Aether Time")

	mHead := systray.AddMenuItem("Starting…", "")
	mHead.Disable()
	mDetail := systray.AddMenuItem("", "")
	mDetail.Disable()
	mDetail.Hide()
	systray.AddSeparator()
	mOpen := systray.AddMenuItem("Open dashboard", "Show Aether Time in your browser")
	mClock := systray.AddMenuItem("Open wall clock", "Full-screen UTC clock")
	mSync := systray.AddMenuItem("Sync now", "Measure every source and correct the clock now")
	mMonitor := systray.AddMenuItemCheckbox("Measure only", "Keep measuring without changing the clock", false)
	mUpdate := systray.AddMenuItem("Install update", "")
	mUpdate.Hide()
	systray.AddSeparator()
	mLogin := systray.AddMenuItemCheckbox("Start at login", "Show this icon when you log in", AutostartEnabled())
	quitLabel := "Close tray icon"
	if opts.InProcess {
		quitLabel = "Quit Aether Time"
	}
	mQuit := systray.AddMenuItem(quitLabel, "")

	started := time.Now()
	refresh := func() {
		st, err := c.status()
		if err != nil {
			st = nil
		}
		v := describe(st, opts.InProcess && time.Since(started) < 20*time.Second)
		if v.icon != icon {
			icon = v.icon
			systray.SetIcon(iconBytes(icon))
		}
		systray.SetTooltip(v.tooltip)
		mHead.SetTitle(v.headline)
		if v.detail != "" {
			mDetail.SetTitle(v.detail)
			mDetail.Show()
		} else {
			mDetail.Hide()
		}
		reachable := st != nil
		for _, m := range []*systray.MenuItem{mOpen, mClock, mSync, mMonitor} {
			if reachable {
				m.Enable()
			} else {
				m.Disable()
			}
		}
		if reachable {
			if st.Engine.Monitor != mMonitor.Checked() {
				if st.Engine.Monitor {
					mMonitor.Check()
				} else {
					mMonitor.Uncheck()
				}
			}
			if st.Engine.Mode == "monitor" {
				mMonitor.Disable() // fixed by the settings
			}
			switch {
			case st.Update.State == "downloading" || st.Update.State == "installing":
				mUpdate.SetTitle("Installing update…")
				mUpdate.Disable()
				mUpdate.Show()
			case st.Update.Available && st.Update.Latest != nil:
				mUpdate.SetTitle("Install update " + st.Update.Latest.Version)
				mUpdate.Enable()
				mUpdate.Show()
			default:
				mUpdate.Hide()
			}
		}
	}
	refresh()
	// onReady must return once the menu is built (the Linux menu is only
	// served after that), so the event loop runs on its own goroutine.
	go loop(opts, c, refresh, mOpen, mClock, mSync, mMonitor, mUpdate, mLogin, mQuit)
}

func loop(opts Options, c *client, refresh func(), mOpen, mClock, mSync, mMonitor, mUpdate, mLogin, mQuit *systray.MenuItem) {
	tick := time.NewTicker(2 * time.Second)
	for {
		select {
		case <-tick.C:
			refresh()
		case <-mOpen.ClickedCh:
			OpenURL(c.base)
		case <-mClock.ClickedCh:
			OpenURL(c.base + "clock")
		case <-mSync.ClickedCh:
			_ = c.post("api/sync", nil)
		case <-mMonitor.ClickedCh:
			_ = c.post("api/monitor", map[string]bool{"on": !mMonitor.Checked()})
			refresh()
		case <-mUpdate.ClickedCh:
			_ = c.post("api/update/install", nil)
		case <-mLogin.ClickedCh:
			if mLogin.Checked() {
				if DisableAutostart() == nil {
					mLogin.Uncheck()
				}
			} else if EnableAutostart() == nil {
				mLogin.Check()
			}
		case <-mQuit.ClickedCh:
			tick.Stop()
			if opts.OnQuit != nil {
				opts.OnQuit()
			}
			systray.Quit()
			return
		}
	}
}
