// SPDX-License-Identifier: GPL-3.0-or-later

// Package icon draws Aether Time's icons (the tray icon with its status badge
// and the application icon) and encodes them as PNG, Windows ICO and BMP.
// Everything is drawn in code, so no image assets need to be kept in sync.
package icon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
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

// Brand colours, shared with the dashboard.
var (
	Dial  = color.NRGBA{0x1D, 0x28, 0x24, 0xFF}
	Face  = color.NRGBA{0xE9, 0xED, 0xE4, 0xFF}
	Lamp  = color.NRGBA{0xE8, 0xA2, 0x3A, 0xFF}
	Line  = color.NRGBA{0x3A, 0x48, 0x42, 0xFF}
	Badge = map[Status]color.NRGBA{
		StatusUnknown: {0x8B, 0x94, 0x8E, 0xFF},
		StatusOK:      {0x3F, 0xB0, 0x76, 0xFF},
		StatusWarn:    Lamp,
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

// shader returns the colour at a point of the unit square, or ok=false for transparent.
type shader func(p vec) (color.NRGBA, bool)

// render draws a shader at size×size pixels with 4×4 supersampling.
func render(size int, f shader) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const ss = 4
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					p := vec{(float64(x) + (float64(sx)+0.5)/ss) / float64(size), (float64(y) + (float64(sy)+0.5)/ss) / float64(size)}
					if c, ok := f(p); ok {
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

// Tray draws the round tray icon with a status badge.
func Tray(size int, st Status) *image.NRGBA {
	return render(size, func(p vec) (color.NRGBA, bool) {
		center := vec{0.5, 0.5}
		badge := vec{0.765, 0.765}
		const badgeR = 0.215
		switch {
		case dist(p, badge) <= badgeR:
			return Badge[st], true
		case dist(p, badge) <= badgeR+0.07:
			return Dial, true // separates the badge from the clock face
		}
		d := dist(p, center)
		if d > 0.48 {
			return color.NRGBA{}, false
		}
		const stroke = 0.09
		if math.Abs(d-0.355) <= stroke/2 { // rim
			return Face, true
		}
		// Hands, as in the logo: up to 12 o'clock, then towards four o'clock.
		if distSegment(p, center, vec{0.5, 0.25}) <= stroke/2 || distSegment(p, center, vec{0.665, 0.61}) <= stroke/2 {
			return Face, true
		}
		return Dial, true
	})
}

// App draws the application icon: the dashboard's clock mark on a rounded
// square, as used for the program file, shortcuts and the installer.
func App(size int) *image.NRGBA {
	return render(size, func(p vec) (color.NRGBA, bool) {
		const corner = 7.0 / 32
		qx := math.Max(math.Abs(p.x-0.5)-(0.5-corner), 0)
		qy := math.Max(math.Abs(p.y-0.5)-(0.5-corner), 0)
		if math.Hypot(qx, qy) > corner {
			return color.NRGBA{}, false
		}
		center := vec{0.5, 0.5}
		const stroke = 2.4 / 32
		if dist(p, center) <= 2.3/32 {
			return Lamp, true
		}
		if math.Abs(dist(p, center)-11.0/32) <= stroke/2 {
			return Face, true
		}
		if distSegment(p, vec{0.5, 8.5 / 32}, center) <= stroke/2 || distSegment(p, center, vec{21.0 / 32, 19.4 / 32}) <= stroke/2 {
			return Face, true
		}
		return Dial, true
	})
}

// PNG encodes an image as PNG.
func PNG(img image.Image) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// IconImage encodes one image the way Windows icon files and icon resources
// store it: PNG for 256 px, a 32-bit DIB with AND mask for smaller sizes
// (the classic form every Windows version and tool understands).
func IconImage(img *image.NRGBA) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w >= 256 {
		return PNG(img)
	}
	maskStride := ((w + 31) / 32) * 4
	var buf bytes.Buffer
	le := binary.LittleEndian
	// BITMAPINFOHEADER; height counts the colour and mask bitmaps together.
	_ = binary.Write(&buf, le, struct {
		Size                   uint32
		Width, Height          int32
		Planes, BitCount       uint16
		Compression, SizeImage uint32
		XPPM, YPPM             int32
		ClrUsed, ClrImportant  uint32
	}{40, int32(w), int32(2 * h), 1, 32, 0, uint32(w*h*4 + maskStride*h), 0, 0, 0, 0})
	for y := h - 1; y >= 0; y-- { // bottom-up BGRA
		for x := 0; x < w; x++ {
			c := img.NRGBAAt(b.Min.X+x, b.Min.Y+y)
			buf.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}
	for y := h - 1; y >= 0; y-- { // AND mask: 1 = transparent
		row := make([]byte, maskStride)
		for x := 0; x < w; x++ {
			if img.NRGBAAt(b.Min.X+x, b.Min.Y+y).A == 0 {
				row[x/8] |= 0x80 >> (x % 8)
			}
		}
		buf.Write(row)
	}
	return buf.Bytes()
}

// ICO encodes images as a Windows .ico file.
func ICO(imgs ...*image.NRGBA) []byte {
	frames := make([][]byte, len(imgs))
	for i, img := range imgs {
		frames[i] = IconImage(img)
	}
	var buf bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&buf, le, [3]uint16{0, 1, uint16(len(imgs))}) // reserved, type=icon, count
	offset := 6 + 16*len(imgs)
	for i, img := range imgs {
		dim := Dim(img.Bounds().Dx())
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

// Dim is the one-byte size used in icon directories (0 means 256).
func Dim(size int) uint8 {
	if size >= 256 {
		return 0
	}
	return uint8(size)
}

// BMP encodes an image as a 24-bit Windows bitmap file, blending
// transparency onto bg (NSIS installer pages need plain BMP).
func BMP(img *image.NRGBA, bg color.NRGBA) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	stride := (w*3 + 3) &^ 3
	var buf bytes.Buffer
	le := binary.LittleEndian
	buf.WriteString("BM")
	_ = binary.Write(&buf, le, []uint32{uint32(54 + stride*h), 0, 54})
	_ = binary.Write(&buf, le, struct {
		Size                   uint32
		Width, Height          int32
		Planes, BitCount       uint16
		Compression, SizeImage uint32
		XPPM, YPPM             int32
		ClrUsed, ClrImportant  uint32
	}{40, int32(w), int32(h), 1, 24, 0, uint32(stride * h), 2835, 2835, 0, 0})
	row := make([]byte, stride)
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			c := img.NRGBAAt(b.Min.X+x, b.Min.Y+y)
			a := float64(c.A) / 255
			mix := func(fg, bgc uint8) uint8 { return uint8(float64(fg)*a + float64(bgc)*(1-a) + 0.5) }
			row[x*3], row[x*3+1], row[x*3+2] = mix(c.B, bg.B), mix(c.G, bg.G), mix(c.R, bg.R)
		}
		buf.Write(row)
	}
	return buf.Bytes()
}

// InstallerSidebar draws the 164×314 picture on the installer's welcome and
// finish pages: the app icon above the dashboard's offset-meter scale.
func InstallerSidebar() *image.NRGBA {
	const w, h = 164, 314
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	fill := func(x0, y0, x1, y1 int, c color.NRGBA) {
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				img.SetNRGBA(x, y, c)
			}
		}
	}
	fill(0, 0, w, h, Dial)
	ic := App(104)
	for y := 0; y < 104; y++ {
		for x := 0; x < 104; x++ {
			c := ic.NRGBAAt(x, y)
			if c.A == 0 {
				continue
			}
			a := float64(c.A) / 255
			bgc := img.NRGBAAt(30+x, 64+y)
			img.SetNRGBA(30+x, 64+y, color.NRGBA{
				uint8(float64(c.R)*a + float64(bgc.R)*(1-a)), uint8(float64(c.G)*a + float64(bgc.G)*(1-a)),
				uint8(float64(c.B)*a + float64(bgc.B)*(1-a)), 255})
		}
	}
	// Log-scale meter: axis, ticks for 1 ms .. 10 s either side, amber needle at zero.
	const axisY, x0, x1 = 244, 16, 148
	cx, half := (x0+x1)/2, (x1-x0)/2
	fill(x0, axisY, x1+1, axisY+1, Face)
	for d := 0; d <= 4; d++ {
		for k := 1; k <= 9 && !(d == 4 && k > 1); k++ {
			f := math.Log10(1+math.Pow(10, float64(d))*float64(k)) / 4
			for _, s := range []int{-1, 1} {
				x := cx + s*int(math.Round(f*float64(half)))
				length := 3
				if k == 1 {
					length = 7
				}
				fill(x, axisY+1, x+1, axisY+1+length, Line)
			}
		}
	}
	fill(cx-1, axisY-26, cx+1, axisY+4, Lamp)
	return img
}
