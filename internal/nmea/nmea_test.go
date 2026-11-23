package nmea

import (
	"math"
	"testing"
	"time"
)

func sentence(body string) string { return "$" + body + "*" + Checksum(body) }

func TestRMC(t *testing.T) {
	r, err := Parse("$GPRMC,123519,A,4807.038,N,01131.000,E,022.4,084.4,230394,003.1,W*6A")
	if err != nil {
		t.Fatal(err)
	}
	if !r.HasTime || r.TimeOfDay != 12*time.Hour+35*time.Minute+19*time.Second {
		t.Errorf("time %v", r.TimeOfDay)
	}
	if !r.HasDate || r.Year != 1994 || r.Month != 3 || r.Day != 23 {
		t.Errorf("date %d-%d-%d", r.Year, r.Month, r.Day)
	}
	if !r.Valid || !r.HasPos || math.Abs(r.Lat-48.1173) > 1e-4 || math.Abs(r.Lon-11.516667) > 1e-4 {
		t.Errorf("pos %v %v %v", r.Valid, r.Lat, r.Lon)
	}
}

func TestBadChecksum(t *testing.T) {
	if _, err := Parse("$GPRMC,123519,A,4807.038,N,01131.000,E,022.4,084.4,230394,003.1,W*6B"); err != ErrChecksum {
		t.Fatalf("want checksum error, got %v", err)
	}
}

func TestZDAAndGGA(t *testing.T) {
	r, err := Parse(sentence("GNZDA,185915.00,23,11,2026,00,00"))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := Resolve(r, time.Now(), false)
	want := time.Date(2026, 11, 23, 18, 59, 15, 0, time.UTC)
	if !ok || !got.Equal(want) {
		t.Fatalf("zda %v want %v", got, want)
	}

	g, err := Parse(sentence("GPGGA,185915.50,3359.000,N,08424.000,W,1,09,0.9,320.5,M,-31.0,M,,"))
	if err != nil {
		t.Fatal(err)
	}
	if !g.Valid || g.Sats != 9 || !g.HasAlt || g.Alt != 320.5 || g.TimeOfDay != 18*time.Hour+59*time.Minute+15500*time.Millisecond {
		t.Fatalf("gga %+v", g)
	}
	if math.Abs(g.Lon+84.4) > 1e-6 {
		t.Fatalf("lon %v", g.Lon)
	}
	nofix, _ := Parse(sentence("GPGGA,185915.00,,,,,0,00,99.9,,M,,M,,"))
	if nofix.Valid || nofix.HasPos {
		t.Fatalf("no-fix GGA should be invalid: %+v", nofix)
	}
}

func TestResolveNearestDay(t *testing.T) {
	// Receiver says 23:59:59 (no date); local clock reads 00:00:01 the next day.
	r := Report{HasTime: true, TimeOfDay: 23*time.Hour + 59*time.Minute + 59*time.Second}
	ref := time.Date(2026, 11, 24, 0, 0, 1, 0, time.UTC)
	got, _ := Resolve(r, ref, false)
	want := time.Date(2026, 11, 23, 23, 59, 59, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// Week-rollover receiver reports 2007 but ignoreDate fixes it.
	r2, _ := Parse(sentence("GPRMC,120000,A,4807.038,N,01131.000,E,0,0,090407,,"))
	got, _ = Resolve(r2, time.Date(2026, 11, 23, 12, 0, 3, 0, time.UTC), true)
	if !got.Equal(time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("ignoreDate: %v", got)
	}
}
