// SPDX-License-Identifier: GPL-3.0-or-later

package tray

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"runtime"
)

// Status selects the colour of the badge on the tray icon.
type Status int

// Tray icon states.
const (
	StatusUnknown Status = iota // not running / starting: grey
	StatusOK                    // synchronized: green
	StatusWarn                  // correcting, or cannot correct: amber
	StatusHold                  // holdover on learned drift: blue
	StatusBad                   // no time source: red
)

var (
	colDial  = color.NRGBA{0x1D, 0x28, 0x24, 0xFF}
	colFace  = color.NRGBA{0xE9, 0xED, 0xE4, 0xFF}
	colBadge = map[Status]color.NRGBA{
		StatusUnknown: {0x8B, 0x94, 0x8E, 0xFF},
		StatusOK:      {0x3F, 0xB0, 0x76, 0xFF},
		StatusWarn:    {0xE8, 0xA2, 0x3A, 0xFF},
		StatusHold:    {0x6F, 0x83, 0xD9, 0xFF},
		StatusBad:     {0xE0, 0x4E, 0x3F, 0xFF},
	}
)

type vec struct{ x, y float64 }

func dist(a, b vec) float64 { return math.Hypot(a.x-b.x, a.y-b.y) }

func distSegment(p, a, b vec) float64 {
	dx, dy := b.x-a.x, b.y-a.y
	t := ((p.x-a.x)*dx + (p.y-a.y)*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	return dist(p, vec{a.x + t*dx, a.y + t*dy})
}

// shade returns the colour at a point of the unit square (painter's order),
// or ok=false for transparent.
func shade(p vec, st Status) (color.NRGBA, bool) {
	center := vec{0.5, 0.5}
	badge := vec{0.765, 0.765}
	const badgeR = 0.215
	switch {
	case dist(p, badge) <= badgeR:
		return colBadge[st], true
	case dist(p, badge) <= badgeR+0.07:
		return colDial, true // separates the badge from the clock face
	}
	d := dist(p, center)
	if d > 0.48 {
		return color.NRGBA{}, false
	}
	const stroke = 0.09
	if math.Abs(d-0.355) <= stroke/2 { // rim
		return colFace, true
	}
	// Hands, as in the logo: up to 12 o'clock, then towards four o'clock.
	if distSegment(p, center, vec{0.5, 0.25}) <= stroke/2 || distSegment(p, center, vec{0.665, 0.61}) <= stroke/2 {
		return colFace, true
	}
	return colDial, true
}

// render draws the icon at size×size pixels with 4×4 supersampling.
func render(size int, st Status) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const ss = 4
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					p := vec{(float64(x) + (float64(sx)+0.5)/ss) / float64(size), (float64(y) + (float64(sy)+0.5)/ss) / float64(size)}
					if c, ok := shade(p, st); ok {
						r += float64(c.R)
						g += float64(c.G)
						b += float64(c.B)
						a++
					}
				}
			}
			if a == 0 {
				continue
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(r/a + 0.5), G: uint8(g/a + 0.5), B: uint8(b/a + 0.5),
				A: uint8(a / (ss * ss) * 255),
			})
		}
	}
	return img
}

// PNG returns the icon as a PNG image.
func PNG(size int, st Status) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, render(size, st))
	return buf.Bytes()
}

// ICO returns a Windows icon holding PNG frames at the given sizes.
func ICO(st Status, sizes ...int) []byte {
	frames := make([][]byte, len(sizes))
	for i, s := range sizes {
		frames[i] = PNG(s, st)
	}
	var buf bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&buf, le, [3]uint16{0, 1, uint16(len(sizes))}) // reserved, type=icon, count
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		dim := uint8(s)
		if s >= 256 {
			dim = 0
		}
		buf.Write([]byte{dim, dim, 0, 0})            // width, height, palette, reserved
		_ = binary.Write(&buf, le, [2]uint16{1, 32}) // planes, bits per pixel
		_ = binary.Write(&buf, le, [2]uint32{uint32(len(frames[i])), uint32(offset)})
		offset += len(frames[i])
	}
	for _, f := range frames {
		buf.Write(f)
	}
	return buf.Bytes()
}

// iconBytes returns the icon in the format the platform's tray expects.
func iconBytes(st Status) []byte {
	if runtime.GOOS == "windows" {
		return ICO(st, 16, 20, 24, 32, 48)
	}
	return PNG(44, st)
}
