// Package config defines Aether Time's configuration, its defaults, and
// where configuration and state files live on each operating system.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Mode controls whether the engine is allowed to change the system clock.
const (
	ModeAuto    = "auto"    // measure and correct the clock
	ModeMonitor = "monitor" // measure only, never touch the clock
)

// Selection strategies for combining sources.
const (
	SelectAuto     = "auto"     // weighted fusion of agreeing sources
	SelectPriority = "priority" // highest-priority healthy source wins
)

// Config is the complete, user-editable configuration. It is stored as JSON.
type Config struct {
	Mode      string          `json:"mode"`
	Selection string          `json:"selection"`
	Adjust    AdjustConfig    `json:"adjust"`
	NTP       NTPConfig       `json:"ntp"`
	HTTPS     HTTPSConfig     `json:"https"`
	GPS       GPSConfig       `json:"gps"`
	GPSD      GPSDConfig      `json:"gpsd"`
	WSJTX     WSJTXConfig     `json:"wsjtx"`
	NTPServer NTPServerConfig `json:"ntp_server"`
	Web       WebConfig       `json:"web"`
	Update    UpdateConfig    `json:"update"`
	Log       LogConfig       `json:"log"`
}

// AdjustConfig controls when and how the system clock is corrected.
type AdjustConfig struct {
	// ThresholdMs: offsets smaller than this are left alone.
	ThresholdMs float64 `json:"threshold_ms"`
	// StepThresholdMs: offsets at or above this are corrected in one step;
	// smaller ones are corrected smoothly when Smooth is enabled.
	StepThresholdMs float64 `json:"step_threshold_ms"`
	// Smooth enables gradual correction in small increments.
	Smooth bool `json:"smooth"`
	// SmoothMaxStepMs is the largest single increment used while smoothing (applied once per second).
	SmoothMaxStepMs float64 `json:"smooth_max_step_ms"`
	// MinIntervalS is the minimum time between two corrections.
	MinIntervalS float64 `json:"min_interval_s"`
	// MaxCorrectionS refuses corrections larger than this (0 = unlimited).
	MaxCorrectionS float64 `json:"max_correction_s"`
	// AllowLargeFirstStep lets the very first correction after start exceed MaxCorrectionS
	// (useful for machines with a dead RTC battery).
	AllowLargeFirstStep bool `json:"allow_large_first_step"`
	// SyncOnStart forces a correction as soon as the first estimate is available.
	SyncOnStart bool `json:"sync_on_start"`
	// DeferDuringTX postpones corrections while WSJT-X/JTDX reports it is transmitting.
	DeferDuringTX bool `json:"defer_during_tx"`
	// Holdover keeps correcting with the learned drift rate when no source is available.
	Holdover bool `json:"holdover"`
}

// NTPConfig configures the NTP (SNTP) client source.
type NTPConfig struct {
	Enabled       bool     `json:"enabled"`
	Servers       []string `json:"servers"`
	PollIntervalS float64  `json:"poll_interval_s"`
	TimeoutMs     float64  `json:"timeout_ms"`
	Samples       int      `json:"samples"`
	CorrectionMs  float64  `json:"correction_ms"`
	Priority      int      `json:"priority"`
}

// HTTPSConfig configures the HTTPS "Date" header fallback source. It works on
// networks that block UDP port 123 (hotels, hotspots, some corporate LANs).
type HTTPSConfig struct {
	Enabled bool     `json:"enabled"`
	URLs    []string `json:"urls"`
	// FallbackOnly only polls when no NTP data is available.
	FallbackOnly  bool    `json:"fallback_only"`
	PollIntervalS float64 `json:"poll_interval_s"`
	CorrectionMs  float64 `json:"correction_ms"`
	Priority      int     `json:"priority"`
}

// GPSConfig configures a GPS receiver that outputs NMEA 0183 sentences.
type GPSConfig struct {
	Enabled bool `json:"enabled"`
	// Transport is "serial", "tcp" (connect to host:port, e.g. a phone app such
	// as GPS2IP / Share GPS) or "udp" (listen on Address).
	Transport string `json:"transport"`
	Port      string `json:"port"` // serial device: COM3, /dev/ttyUSB0, /dev/cu.usbserial-…
	Baud      int    `json:"baud"`
	DataBits  int    `json:"data_bits"`
	Parity    string `json:"parity"`    // none, odd, even, mark, space
	StopBits  string `json:"stop_bits"` // "1", "1.5", "2"
	RTS       bool   `json:"rts"`
	DTR       bool   `json:"dtr"`
	Address   string `json:"address"` // for tcp/udp transports
	// LatencyMs is the delay between the top of the second and the arrival of
	// the first NMEA sentence. It is learned automatically when AutoCalibrate
	// is on and a network source is available.
	LatencyMs     float64 `json:"latency_ms"`
	AutoCalibrate bool    `json:"auto_calibrate"`
	// TimeOnly ignores the date reported by the receiver (old receivers affected
	// by the GPS week-number rollover) and takes the date from the system clock.
	TimeOnly     bool    `json:"time_only"`
	CorrectionMs float64 `json:"correction_ms"`
	Priority     int     `json:"priority"`
}

// GPSDConfig configures a gpsd daemon source (Linux/macOS, Raspberry Pi).
// PPS and TOFF reports from gpsd are used when available.
type GPSDConfig struct {
	Enabled      bool    `json:"enabled"`
	Address      string  `json:"address"`
	CorrectionMs float64 `json:"correction_ms"`
	Priority     int     `json:"priority"`
}

// WSJTXConfig configures the FT8/FT4 decode-timing source that listens to the
// WSJT-X UDP protocol (also spoken by JTDX and MSHV) and/or tails ALL.TXT.
type WSJTXConfig struct {
	Enabled bool `json:"enabled"`
	// Listen is the UDP address WSJT-X sends to. Multicast groups are joined automatically.
	Listen string `json:"listen"`
	// Forward relays every datagram (both directions) to these addresses so
	// JTAlert, GridTracker, loggers, etc. keep working.
	Forward []string `json:"forward"`
	// AllTxtPath optionally tails an ALL.TXT decode log instead of / in addition to UDP.
	AllTxtPath string   `json:"all_txt_path"`
	Modes      []string `json:"modes"`
	MinSNR     int      `json:"min_snr"`
	WindowS    float64  `json:"window_s"`
	MinDecodes int      `json:"min_decodes"`
	// DTBiasMs is the median DT you see when your clock is exactly right
	// (audio chain latency + the habits of other stations).
	DTBiasMs float64 `json:"dt_bias_ms"`
	// LearnBias measures DTBiasMs automatically while a better source is available.
	LearnBias bool `json:"learn_bias"`
	// AutoGrid sends the GPS-derived Maidenhead locator to WSJT-X.
	AutoGrid bool `json:"auto_grid"`
	Priority int  `json:"priority"`
}

// NTPServerConfig configures the built-in NTP server that shares the
// disciplined time with other computers on the LAN (field day, DXpeditions).
type NTPServerConfig struct {
	Enabled bool   `json:"enabled"`
	Listen  string `json:"listen"`
}

// WebConfig configures the dashboard and REST API.
type WebConfig struct {
	Listen      string `json:"listen"`
	OpenBrowser bool   `json:"open_browser"`
	// Password is required for API access from non-loopback addresses.
	Password string `json:"password"`
}

// UpdateConfig configures the automatic updater.
type UpdateConfig struct {
	Enabled bool `json:"enabled"`
	// AutoInstall downloads, verifies and installs updates without asking.
	AutoInstall    bool    `json:"auto_install"`
	Channel        string  `json:"channel"` // stable | beta
	CheckIntervalH float64 `json:"check_interval_h"`
	Repo           string  `json:"repo"`
	APIBase        string  `json:"api_base"`
}

// LogConfig configures logging.
type LogConfig struct {
	SyncLog bool `json:"sync_log"`
	Debug   bool `json:"debug"`
}

// Default returns the factory configuration.
func Default() Config {
	return Config{
		Mode:      ModeAuto,
		Selection: SelectAuto,
		Adjust: AdjustConfig{
			ThresholdMs:         20,
			StepThresholdMs:     500,
			Smooth:              true,
			SmoothMaxStepMs:     25,
			MinIntervalS:        30,
			MaxCorrectionS:      0,
			AllowLargeFirstStep: true,
			SyncOnStart:         true,
			DeferDuringTX:       true,
			Holdover:            true,
		},
		NTP: NTPConfig{
			Enabled: true,
			Servers: []string{
				"0.pool.ntp.org",
				"1.pool.ntp.org",
				"2.pool.ntp.org",
				"time.cloudflare.com",
				"time.nist.gov",
			},
			PollIntervalS: 300,
			TimeoutMs:     2500,
			Samples:       4,
			Priority:      2,
		},
		HTTPS: HTTPSConfig{
			Enabled:       true,
			URLs:          []string{"https://www.google.com/generate_204", "https://www.cloudflare.com/cdn-cgi/trace"},
			FallbackOnly:  true,
			PollIntervalS: 900,
			Priority:      5,
		},
		GPS: GPSConfig{
			Enabled:       false,
			Transport:     "serial",
			Port:          defaultSerialPort(),
			Baud:          9600,
			DataBits:      8,
			Parity:        "none",
			StopBits:      "1",
			DTR:           true,
			RTS:           false,
			Address:       "",
			LatencyMs:     0,
			AutoCalibrate: true,
			Priority:      1,
		},
		GPSD: GPSDConfig{
			Enabled:  false,
			Address:  "127.0.0.1:2947",
			Priority: 1,
		},
		WSJTX: WSJTXConfig{
			Enabled:    true,
			Listen:     "127.0.0.1:2237",
			Modes:      []string{"FT8", "FT4"},
			MinSNR:     -24,
			WindowS:    240,
			MinDecodes: 8,
			DTBiasMs:   0,
			LearnBias:  true,
			AutoGrid:   false,
			Priority:   3,
		},
		NTPServer: NTPServerConfig{
			Enabled: false,
			Listen:  ":123",
		},
		Web: WebConfig{
			Listen:      "127.0.0.1:8123",
			OpenBrowser: true,
		},
		Update: UpdateConfig{
			Enabled:        true,
			AutoInstall:    false,
			Channel:        "stable",
			CheckIntervalH: 24,
			Repo:           "KK4ODA/Aether-Time",
			APIBase:        "https://api.github.com",
		},
		Log: LogConfig{SyncLog: true},
	}
}

func defaultSerialPort() string {
	switch runtime.GOOS {
	case "windows":
		return "COM3"
	case "darwin":
		return "/dev/cu.usbserial"
	default:
		return "/dev/ttyUSB0"
	}
}

// Validate checks the configuration and returns a descriptive error for the
// first problem found.
func (c *Config) Validate() error {
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }

	if c.Mode != ModeAuto && c.Mode != ModeMonitor {
		add("mode must be %q or %q", ModeAuto, ModeMonitor)
	}
	if c.Selection != SelectAuto && c.Selection != SelectPriority {
		add("selection must be %q or %q", SelectAuto, SelectPriority)
	}
	a := c.Adjust
	if a.ThresholdMs < 0 || a.StepThresholdMs < 0 || a.SmoothMaxStepMs < 0 || a.MinIntervalS < 0 || a.MaxCorrectionS < 0 {
		add("adjust values must not be negative")
	}
	if a.Smooth && a.SmoothMaxStepMs <= 0 {
		add("adjust.smooth_max_step_ms must be > 0 when smoothing is enabled")
	}
	if c.NTP.Enabled {
		if len(c.NTP.Servers) == 0 {
			add("ntp.servers must list at least one server")
		}
		if c.NTP.PollIntervalS < 16 {
			add("ntp.poll_interval_s must be at least 16 seconds (be kind to public servers)")
		}
		if c.NTP.Samples < 1 || c.NTP.Samples > 8 {
			add("ntp.samples must be between 1 and 8")
		}
	}
	if c.HTTPS.Enabled {
		if len(c.HTTPS.URLs) == 0 {
			add("https.urls must list at least one URL")
		}
		for _, u := range c.HTTPS.URLs {
			if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
				add("https.urls entry %q must start with https://", u)
			}
		}
		if c.HTTPS.PollIntervalS < 60 {
			add("https.poll_interval_s must be at least 60 seconds")
		}
	}
	if c.GPS.Enabled {
		switch c.GPS.Transport {
		case "serial":
			if c.GPS.Port == "" {
				add("gps.port is required for the serial transport")
			}
			if c.GPS.Baud <= 0 {
				add("gps.baud must be positive")
			}
			if c.GPS.DataBits < 5 || c.GPS.DataBits > 8 {
				add("gps.data_bits must be 5..8")
			}
			switch c.GPS.Parity {
			case "none", "odd", "even", "mark", "space":
			default:
				add("gps.parity must be none, odd, even, mark or space")
			}
			switch c.GPS.StopBits {
			case "1", "1.5", "2":
			default:
				add("gps.stop_bits must be 1, 1.5 or 2")
			}
		case "tcp", "udp":
			if _, _, err := net.SplitHostPort(c.GPS.Address); err != nil {
				add("gps.address must be host:port for the %s transport", c.GPS.Transport)
			}
		default:
			add("gps.transport must be serial, tcp or udp")
		}
	}
	if c.GPSD.Enabled {
		if _, _, err := net.SplitHostPort(c.GPSD.Address); err != nil {
			add("gpsd.address must be host:port")
		}
	}
	if c.WSJTX.Enabled {
		if c.WSJTX.Listen == "" && c.WSJTX.AllTxtPath == "" {
			add("wsjtx needs a listen address or an all_txt_path")
		}
		if c.WSJTX.Listen != "" {
			if _, _, err := net.SplitHostPort(c.WSJTX.Listen); err != nil {
				add("wsjtx.listen must be host:port")
			}
		}
		for _, f := range c.WSJTX.Forward {
			if _, _, err := net.SplitHostPort(f); err != nil {
				add("wsjtx.forward entry %q must be host:port", f)
			}
		}
		if c.WSJTX.MinDecodes < 1 {
			add("wsjtx.min_decodes must be at least 1")
		}
		if c.WSJTX.WindowS < 30 {
			add("wsjtx.window_s must be at least 30 seconds")
		}
	}
	if c.NTPServer.Enabled {
		if _, _, err := net.SplitHostPort(c.NTPServer.Listen); err != nil {
			add("ntp_server.listen must be host:port, e.g. :123")
		}
	}
	if _, _, err := net.SplitHostPort(c.Web.Listen); err != nil {
		add("web.listen must be host:port, e.g. 127.0.0.1:8123")
	} else if !IsLoopbackListen(c.Web.Listen) && c.Web.Password == "" {
		add("web.password is required when web.listen is not a loopback address")
	}
	if c.Update.Channel != "stable" && c.Update.Channel != "beta" {
		add("update.channel must be stable or beta")
	}
	if c.Update.Enabled && c.Update.CheckIntervalH < 1 {
		add("update.check_interval_h must be at least 1")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// IsLoopbackListen reports whether a listen address only accepts local connections.
func IsLoopbackListen(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Load reads a configuration file. Missing fields keep their defaults, so old
// config files keep working after upgrades. A missing file yields the defaults.
func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Default(), fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, cfg.Validate()
}

// Save writes the configuration atomically.
func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, append(data, '\n'), 0o600)
}

// WriteFileAtomic writes data to a temp file in the same directory and renames it into place.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
