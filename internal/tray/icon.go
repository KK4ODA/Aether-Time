// SPDX-License-Identifier: GPL-3.0-or-later

package tray

import (
	"image"
	"runtime"

	"github.com/KK4ODA/Aether-Time/internal/icon"
)

// Status selects the colour of the badge on the tray icon.
type Status = icon.Status

// Tray icon states.
const (
	StatusUnknown = icon.StatusUnknown
	StatusOK      = icon.StatusOK
	StatusWarn    = icon.StatusWarn
	StatusHold    = icon.StatusHold
	StatusBad     = icon.StatusBad
)

// iconBytes returns the icon in the format the platform's tray expects.
func iconBytes(st Status) []byte {
	if runtime.GOOS == "windows" {
		var imgs []*image.NRGBA
		for _, s := range []int{16, 20, 24, 32, 48} {
			imgs = append(imgs, icon.Tray(s, st))
		}
		return icon.ICO(imgs...)
	}
	return icon.PNG(icon.Tray(44, st))
}
