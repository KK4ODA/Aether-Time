// SPDX-License-Identifier: GPL-3.0-or-later

// Package maidenhead converts coordinates to Maidenhead grid locators.
package maidenhead

import (
	"errors"
	"math"
)

// Locator returns the locator for lat/lon with the given precision (2, 4, 6 or 8 characters).
func Locator(lat, lon float64, chars int) (string, error) {
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 || math.IsNaN(lat) || math.IsNaN(lon) {
		return "", errors.New("maidenhead: coordinates out of range")
	}
	if chars != 2 && chars != 4 && chars != 6 && chars != 8 {
		return "", errors.New("maidenhead: precision must be 2, 4, 6 or 8")
	}
	x := lon + 180
	y := lat + 90
	if x >= 360 {
		x = 359.999999
	}
	if y >= 180 {
		y = 179.999999
	}
	out := make([]byte, 0, 8)
	// Field: 20° x 10°
	fx, fy := int(x/20), int(y/10)
	out = append(out, byte('A'+fx), byte('A'+fy))
	x -= float64(fx) * 20
	y -= float64(fy) * 10
	if chars >= 4 { // Square: 2° x 1°
		sx, sy := int(x/2), int(y/1)
		out = append(out, byte('0'+sx), byte('0'+sy))
		x -= float64(sx) * 2
		y -= float64(sy)
	}
	if chars >= 6 { // Subsquare: 5' x 2.5'
		ssx, ssy := int(x/(2.0/24)), int(y/(1.0/24))
		out = append(out, byte('a'+ssx), byte('a'+ssy))
		x -= float64(ssx) * (2.0 / 24)
		y -= float64(ssy) * (1.0 / 24)
	}
	if chars >= 8 { // Extended square
		ex, ey := int(x/(2.0/240)), int(y/(1.0/240))
		out = append(out, byte('0'+ex), byte('0'+ey))
	}
	return string(out), nil
}
