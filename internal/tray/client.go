package tray

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

// client talks to the running engine through its local API, so the tray works
// both inside the same process and next to the system service.
type client struct {
	base string
	hc   *http.Client
}

func newClient(base string) *client {
	return &client{base: strings.TrimRight(base, "/") + "/", hc: &http.Client{Timeout: 3 * time.Second}}
}

type apiStatus struct {
	Engine struct {
		State        string   `json:"state"`
		Mode         string   `json:"mode"`
		Monitor      bool     `json:"monitor"`
		CanAdjust    bool     `json:"can_adjust"`
		OffsetMs     *float64 `json:"offset_ms"`
		ErrorMs      *float64 `json:"error_ms"`
		Used         []string `json:"used"`
		Transmitting bool     `json:"transmitting"`
	} `json:"engine"`
	Update struct {
		Available bool   `json:"available"`
		State     string `json:"state"`
		Latest    *struct {
			Version string `json:"version"`
		} `json:"latest"`
	} `json:"update"`
	Info struct {
		Version string `json:"version"`
	} `json:"info"`
}

func (c *client) status() (*apiStatus, error) {
	resp, err := c.hc.Get(c.base + "api/status")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status: %s", resp.Status)
	}
	var st apiStatus
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (c *client) post(path string, body any) error {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req, err := http.NewRequest(http.MethodPost, c.base+strings.TrimLeft(path, "/"), bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("X-Aether", "1")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", path, resp.Status)
	}
	return nil
}

var sourceNames = map[string]string{"ntp": "NTP", "https": "web time", "gps": "GPS", "gpsd": "gpsd", "wsjtx": "FT8 decodes"}

// view is what the tray shows for a status (nil status = engine unreachable).
type view struct {
	icon     Status
	headline string
	detail   string
	tooltip  string
}

func describe(st *apiStatus, starting bool) view {
	if st == nil {
		if starting {
			return view{icon: StatusUnknown, headline: "Starting…", tooltip: "Aether Time: starting"}
		}
		return view{icon: StatusUnknown, headline: "Aether Time is not running", tooltip: "Aether Time: not running"}
	}
	e := st.Engine
	var v view
	switch e.State {
	case "synced":
		v.icon, v.headline = StatusOK, "Synchronized"
		if len(e.Used) > 0 {
			names := make([]string, len(e.Used))
			for i, id := range e.Used {
				if n, ok := sourceNames[id]; ok {
					names[i] = n
				} else {
					names[i] = id
				}
			}
			v.headline += " with " + strings.Join(names, " + ")
		}
	case "tracking":
		v.icon, v.headline = StatusWarn, "Correcting the clock"
	case "holdover":
		v.icon, v.headline = StatusHold, "Holdover: following the learned drift"
	case "no-reference":
		v.icon, v.headline = StatusBad, "No time source available"
	default:
		v.icon, v.headline = StatusUnknown, "Starting…"
	}
	if e.Monitor {
		v.headline += " (measuring only)"
	} else if !e.CanAdjust && e.Mode == "auto" && v.icon == StatusOK {
		v.icon = StatusWarn
		v.headline += " (can't change the clock)"
	}
	short := strings.SplitN(v.headline, " with ", 2)[0]
	if e.OffsetMs != nil && e.ErrorMs != nil {
		errMs := -*e.OffsetMs // positive: this computer is ahead
		v.detail = fmt.Sprintf("Clock error %s ± %s", fmtMs(errMs, true), fmtMs(*e.ErrorMs, false))
		v.tooltip = fmt.Sprintf("Aether Time: %s, %s", strings.ToLower(short[:1])+short[1:], fmtMs(errMs, true))
	} else {
		v.tooltip = "Aether Time: " + strings.ToLower(short[:1]) + short[1:]
	}
	if len(v.tooltip) > 120 {
		v.tooltip = v.tooltip[:120]
	}
	return v
}

func fmtMs(v float64, signed bool) string {
	a := math.Abs(v)
	var s string
	switch {
	case a >= 1000:
		s = fmt.Sprintf("%.3f s", a/1000)
	case a >= 100:
		s = fmt.Sprintf("%.0f ms", a)
	case a >= 10:
		s = fmt.Sprintf("%.1f ms", a)
	default:
		s = fmt.Sprintf("%.2f ms", a)
	}
	if !signed {
		return s
	}
	switch {
	case v > 0:
		return "+" + s
	case v < 0:
		return "−" + s
	}
	return s
}
