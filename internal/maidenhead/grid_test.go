// SPDX-License-Identifier: GPL-3.0-or-later

package maidenhead

import "testing"

func TestLocator(t *testing.T) {
	cases := []struct {
		lat, lon float64
		n        int
		want     string
	}{
		{48.1173, 11.5167, 6, "JN58sc"},   // Munich
		{41.7147, -72.7272, 6, "FN31pr"},  // ARRL HQ, Newington CT
		{-33.8688, 151.2093, 6, "QF56od"}, // Sydney
		{33.75, -84.39, 4, "EM73"},        // Atlanta
		{41.7147, -72.7272, 8, "FN31pr21"},
	}
	for _, c := range cases {
		got, err := Locator(c.lat, c.lon, c.n)
		if err != nil || got != c.want {
			t.Errorf("Locator(%v,%v,%d) = %q, %v; want %q", c.lat, c.lon, c.n, got, err, c.want)
		}
	}
	if _, err := Locator(91, 0, 6); err == nil {
		t.Error("expected range error")
	}
}
