// SPDX-License-Identifier: GPL-3.0-or-later

package icon

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"testing"
)

func TestTrayBadgeColours(t *testing.T) {
	for st, want := range Badge {
		img := Tray(32, st)
		if c := img.NRGBAAt(24, 24); c != want {
			t.Fatalf("status %d: badge colour %v, want %v", st, c, want)
		}
		if img.NRGBAAt(0, 0).A != 0 {
			t.Fatal("corner not transparent")
		}
	}
}

func TestAppIcon(t *testing.T) {
	img := App(64)
	if img.NRGBAAt(0, 0).A != 0 {
		t.Fatal("rounded corner should be transparent")
	}
	if img.NRGBAAt(4, 32) != Dial {
		t.Fatalf("edge colour %v", img.NRGBAAt(4, 32))
	}
	if img.NRGBAAt(32, 32) != Lamp {
		t.Fatalf("centre colour %v", img.NRGBAAt(32, 32))
	}
}

func TestIconImageDIB(t *testing.T) {
	img := App(16)
	b := IconImage(img)
	le := binary.LittleEndian
	if le.Uint32(b) != 40 || le.Uint32(b[4:]) != 16 || le.Uint32(b[8:]) != 32 || le.Uint16(b[14:]) != 32 {
		t.Fatalf("header % x", b[:16])
	}
	if len(b) != 40+16*16*4+16*4 {
		t.Fatalf("size %d", len(b))
	}
	// Bottom-up rows: the first pixel stored is the bottom-left corner (transparent).
	if b[40+3] != 0 {
		t.Fatal("bottom-left pixel should be transparent")
	}
	// AND mask marks it transparent too.
	mask := b[40+16*16*4:]
	if mask[0]&0x80 == 0 {
		t.Fatal("mask bit not set for a transparent pixel")
	}
}

func TestICOMixesDIBAndPNG(t *testing.T) {
	ico := ICO(App(256), App(16))
	le := binary.LittleEndian
	if le.Uint16(ico[4:]) != 2 || ico[6] != 0 || ico[6+16] != 16 {
		t.Fatalf("directory % x", ico[:38])
	}
	off := le.Uint32(ico[6+12:])
	if _, err := png.Decode(bytes.NewReader(ico[off:])); err != nil {
		t.Fatalf("256 px frame should be PNG: %v", err)
	}
	off2 := le.Uint32(ico[6+16+12:])
	if le.Uint32(ico[off2:]) != 40 {
		t.Fatal("16 px frame should be a DIB")
	}
}

func TestBMP(t *testing.T) {
	b := BMP(InstallerSidebar(), Dial)
	le := binary.LittleEndian
	if string(b[:2]) != "BM" || int(le.Uint32(b[2:])) != len(b) || le.Uint32(b[18:]) != 164 || le.Uint32(b[22:]) != 314 || le.Uint16(b[28:]) != 24 {
		t.Fatalf("bitmap header % x", b[:30])
	}
}
