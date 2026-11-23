// Package nmea parses the NMEA 0183 sentences that carry time and position:
// RMC, ZDA, GGA, GLL and GSV, from any talker (GP, GN, GL, GA, BD, ...).
package nmea

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Report is the information extracted from one sentence.
type Report struct {
	Type      string // RMC, ZDA, GGA, GLL, GSV
	Talker    string
	HasTime   bool
	TimeOfDay time.Duration // since 00:00:00 UTC
	HasDate   bool
	Year      int
	Month     int
	Day       int
	// Valid is the receiver's own claim that the fix (and therefore the time) is good.
	// It is only meaningful when HasValidity is true.
	HasValidity bool
	Valid       bool
	HasPos      bool
	Lat, Lon    float64
	HasAlt      bool
	Alt         float64
	Quality     int // GGA fix quality
	Sats        int // satellites used (GGA) or in view (GSV)
}

// Errors returned by Parse.
var (
	ErrNotNMEA     = errors.New("nmea: not a sentence")
	ErrChecksum    = errors.New("nmea: bad checksum")
	ErrUnsupported = errors.New("nmea: unsupported sentence")
)

// Parse decodes one sentence. The checksum is verified when present.
func Parse(line string) (Report, error) {
	line = strings.TrimSpace(line)
	if len(line) < 6 || (line[0] != '$' && line[0] != '!') {
		return Report{}, ErrNotNMEA
	}
	body := line[1:]
	if i := strings.IndexByte(body, '*'); i >= 0 {
		sum := body[i+1:]
		body = body[:i]
		if len(sum) < 2 {
			return Report{}, ErrChecksum
		}
		want, err := strconv.ParseUint(sum[:2], 16, 8)
		if err != nil {
			return Report{}, ErrChecksum
		}
		var got byte
		for j := 0; j < len(body); j++ {
			got ^= body[j]
		}
		if byte(want) != got {
			return Report{}, ErrChecksum
		}
	}
	f := strings.Split(body, ",")
	if len(f[0]) < 5 {
		return Report{}, ErrNotNMEA
	}
	r := Report{Talker: f[0][:len(f[0])-3], Type: f[0][len(f[0])-3:]}
	f = f[1:]
	get := func(i int) string {
		if i < len(f) {
			return f[i]
		}
		return ""
	}
	var err error
	switch r.Type {
	case "RMC":
		// time, status, lat, N/S, lon, E/W, speed, course, date, magvar, E/W, mode
		if r.TimeOfDay, r.HasTime, err = parseTime(get(0)); err != nil {
			return r, err
		}
		r.HasValidity = true
		r.Valid = get(1) == "A"
		if mode := get(11); mode == "N" {
			r.Valid = false
		}
		r.Lat, r.Lon, r.HasPos = parseLatLon(get(2), get(3), get(4), get(5))
		if d := get(8); d != "" {
			if r.Day, r.Month, r.Year, err = parseDDMMYY(d); err != nil {
				return r, err
			}
			r.HasDate = true
		}
	case "ZDA":
		// time, day, month, year, local zone hours, local zone minutes
		if r.TimeOfDay, r.HasTime, err = parseTime(get(0)); err != nil {
			return r, err
		}
		if get(1) != "" && get(2) != "" && get(3) != "" {
			r.Day, _ = strconv.Atoi(get(1))
			r.Month, _ = strconv.Atoi(get(2))
			r.Year, _ = strconv.Atoi(get(3))
			if r.Day < 1 || r.Day > 31 || r.Month < 1 || r.Month > 12 || r.Year < 1980 {
				return r, fmt.Errorf("nmea: bad ZDA date %s/%s/%s", get(1), get(2), get(3))
			}
			r.HasDate = true
		}
	case "GGA":
		// time, lat, N/S, lon, E/W, quality, sats, hdop, alt, M, ...
		if r.TimeOfDay, r.HasTime, err = parseTime(get(0)); err != nil {
			return r, err
		}
		r.Lat, r.Lon, r.HasPos = parseLatLon(get(1), get(2), get(3), get(4))
		r.Quality, _ = strconv.Atoi(get(5))
		r.HasValidity = true
		r.Valid = r.Quality > 0
		r.Sats, _ = strconv.Atoi(get(6))
		if a, err := strconv.ParseFloat(get(8), 64); err == nil {
			r.Alt, r.HasAlt = a, true
		}
		if !r.Valid {
			r.HasPos = false
		}
	case "GLL":
		// lat, N/S, lon, E/W, time, status, mode
		r.Lat, r.Lon, r.HasPos = parseLatLon(get(0), get(1), get(2), get(3))
		if r.TimeOfDay, r.HasTime, err = parseTime(get(4)); err != nil {
			return r, err
		}
		r.HasValidity = true
		r.Valid = get(5) == "A"
		if !r.Valid {
			r.HasPos = false
		}
	case "GSV":
		r.Sats, _ = strconv.Atoi(get(2))
	default:
		return r, ErrUnsupported
	}
	return r, nil
}

func parseTime(s string) (time.Duration, bool, error) {
	if s == "" {
		return 0, false, nil
	}
	if len(s) < 6 {
		return 0, false, fmt.Errorf("nmea: bad time %q", s)
	}
	h, err1 := strconv.Atoi(s[0:2])
	m, err2 := strconv.Atoi(s[2:4])
	sec, err3 := strconv.ParseFloat(s[4:], 64)
	if err1 != nil || err2 != nil || err3 != nil || h > 23 || m > 59 || sec >= 61 {
		return 0, false, fmt.Errorf("nmea: bad time %q", s)
	}
	d := time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec*float64(time.Second)+0.5)
	return d, true, nil
}

func parseDDMMYY(s string) (day, month, year int, err error) {
	if len(s) != 6 {
		return 0, 0, 0, fmt.Errorf("nmea: bad date %q", s)
	}
	day, err1 := strconv.Atoi(s[0:2])
	month, err2 := strconv.Atoi(s[2:4])
	yy, err3 := strconv.Atoi(s[4:6])
	if err1 != nil || err2 != nil || err3 != nil || day < 1 || day > 31 || month < 1 || month > 12 {
		return 0, 0, 0, fmt.Errorf("nmea: bad date %q", s)
	}
	year = 2000 + yy
	if yy >= 80 {
		year = 1900 + yy
	}
	return day, month, year, nil
}

func parseLatLon(lat, ns, lon, ew string) (float64, float64, bool) {
	la, ok1 := parseDegMin(lat, 2)
	lo, ok2 := parseDegMin(lon, 3)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	if ns == "S" {
		la = -la
	}
	if ew == "W" {
		lo = -lo
	}
	if la < -90 || la > 90 || lo < -180 || lo > 180 {
		return 0, 0, false
	}
	return la, lo, true
}

func parseDegMin(s string, degDigits int) (float64, bool) {
	if len(s) < degDigits+2 {
		return 0, false
	}
	deg, err1 := strconv.Atoi(s[:degDigits])
	min, err2 := strconv.ParseFloat(s[degDigits:], 64)
	if err1 != nil || err2 != nil || min >= 60 {
		return 0, false
	}
	return float64(deg) + min/60, true
}

// Resolve turns a time of day into a full UTC time.
//
// If the report carries a date and ignoreDate is false, that date is used.
// Otherwise the date is chosen from the reference time (normally the local
// clock), picking yesterday/today/tomorrow so that the result is within 12
// hours of the reference. This also corrects receivers that report a wrong
// date because of the GPS week-number rollover.
func Resolve(r Report, ref time.Time, ignoreDate bool) (time.Time, bool) {
	if !r.HasTime {
		return time.Time{}, false
	}
	if r.HasDate && !ignoreDate {
		return time.Date(r.Year, time.Month(r.Month), r.Day, 0, 0, 0, 0, time.UTC).Add(r.TimeOfDay), true
	}
	ref = ref.UTC()
	day := time.Date(ref.Year(), ref.Month(), ref.Day(), 0, 0, 0, 0, time.UTC)
	best := time.Time{}
	for _, d := range []int{-1, 0, 1} {
		c := day.AddDate(0, 0, d).Add(r.TimeOfDay)
		if best.IsZero() || absDur(c.Sub(ref)) < absDur(best.Sub(ref)) {
			best = c
		}
	}
	return best, true
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// Checksum returns the two-digit checksum for a sentence body (without $ and *).
func Checksum(body string) string {
	var c byte
	for i := 0; i < len(body); i++ {
		c ^= body[i]
	}
	return fmt.Sprintf("%02X", c)
}
