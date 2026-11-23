// SPDX-License-Identifier: GPL-3.0-or-later

package winres

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"image"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/KK4ODA/Aether-Time/internal/icon"
)

func testResources() Resources {
	return Resources{
		Icons: []*image.NRGBA{icon.App(32), icon.App(16)},
		Version: Version{
			File:    [4]uint16{1, 2, 3, 0},
			Product: [4]uint16{1, 2, 3, 0},
			Strings: map[string]string{"ProductName": "Aether Time", "FileVersion": "1.2.3-beta.1"},
		},
		Manifest: `<?xml version="1.0"?><assembly/>`,
	}
}

func utf16le(s string) []byte {
	var b bytes.Buffer
	for _, c := range utf16.Encode([]rune(s)) {
		binary.Write(&b, binary.LittleEndian, c)
	}
	return b.Bytes()
}

// walk follows the resource directory and returns type -> id -> data.
func walk(t *testing.T, sect []byte) map[uint32]map[uint32][]byte {
	t.Helper()
	le := binary.LittleEndian
	entries := func(at uint32) [][2]uint32 {
		n := uint32(le.Uint16(sect[at+12:])) + uint32(le.Uint16(sect[at+14:]))
		var out [][2]uint32
		for i := uint32(0); i < n; i++ {
			out = append(out, [2]uint32{le.Uint32(sect[at+16+8*i:]), le.Uint32(sect[at+16+8*i+4:])})
		}
		return out
	}
	out := map[uint32]map[uint32][]byte{}
	var prevType uint32
	for _, te := range entries(0) {
		if te[0] < prevType {
			t.Fatal("type entries not sorted")
		}
		prevType = te[0]
		if te[1]&0x80000000 == 0 {
			t.Fatal("type entry must point to a subdirectory")
		}
		out[te[0]] = map[uint32][]byte{}
		for _, ie := range entries(te[1] &^ 0x80000000) {
			langs := entries(ie[1] &^ 0x80000000)
			if len(langs) != 1 || langs[0][0] != langEnUS {
				t.Fatalf("languages %v", langs)
			}
			de := langs[0][1]
			off, size := le.Uint32(sect[de:]), le.Uint32(sect[de+4:])
			out[te[0]][ie[0]] = sect[off : off+size]
		}
	}
	return out
}

func TestObjectParsesAsCOFF(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "386"} {
		obj, err := Object(arch, testResources())
		if err != nil {
			t.Fatal(err)
		}
		f, err := pe.NewFile(bytes.NewReader(obj))
		if err != nil {
			t.Fatalf("%s: %v", arch, err)
		}
		if len(f.Sections) != 1 || f.Sections[0].Name != ".rsrc" {
			t.Fatalf("%s: sections %v", arch, f.Sections)
		}
		s := f.Sections[0]
		// 2 icons + group + version + manifest = 5 data entries to relocate.
		if len(s.Relocs) != 5 {
			t.Fatalf("%s: %d relocations", arch, len(s.Relocs))
		}
		if len(f.COFFSymbols) != 1 || f.COFFSymbols[0].StorageClass != 3 || f.COFFSymbols[0].SectionNumber != 1 {
			t.Fatalf("%s: symbols %+v", arch, f.COFFSymbols)
		}
		data, _ := s.Data()
		res := walk(t, data)
		if len(res[rtIcon]) != 2 || len(res[rtGroupIcon]) != 1 {
			t.Fatalf("%s: icons %d groups %d", arch, len(res[rtIcon]), len(res[rtGroupIcon]))
		}
		if string(res[rtManifest][1]) != testResources().Manifest {
			t.Fatalf("%s: manifest %q", arch, res[rtManifest][1])
		}
		ver := res[rtVersion][1]
		if int(binary.LittleEndian.Uint16(ver)) != len(ver) {
			t.Fatalf("%s: version wLength %d, size %d", arch, binary.LittleEndian.Uint16(ver), len(ver))
		}
		for _, want := range []string{"VS_VERSION_INFO", "StringFileInfo", "040904B0", "ProductName", "Aether Time", "1.2.3-beta.1", "Translation"} {
			if !bytes.Contains(ver, utf16le(want)) {
				t.Fatalf("%s: version info lacks %q", arch, want)
			}
		}
		// VS_FIXEDFILEINFO: signature, then file version 1.2.3.0.
		i := bytes.Index(ver, []byte{0xBD, 0x04, 0xEF, 0xFE})
		if i < 0 || binary.LittleEndian.Uint32(ver[i+8:]) != 1<<16|2 || binary.LittleEndian.Uint32(ver[i+12:]) != 3<<16 {
			t.Fatalf("%s: fixed file info missing or wrong", arch)
		}
		// Each relocated data entry holds the offset of its own data.
		for _, r := range s.Relocs {
			off := binary.LittleEndian.Uint32(data[r.VirtualAddress:])
			if off%8 != 0 || int(off) >= len(data) {
				t.Fatalf("%s: bad data offset %d", arch, off)
			}
		}
	}
	if _, err := Object("mips", testResources()); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatal("expected unsupported architecture error")
	}
}

func TestGroupIconDirectory(t *testing.T) {
	obj, _ := Object("amd64", testResources())
	f, _ := pe.NewFile(bytes.NewReader(obj))
	data, _ := f.Sections[0].Data()
	res := walk(t, data)
	g := res[rtGroupIcon][1]
	le := binary.LittleEndian
	if le.Uint16(g[2:]) != 1 || le.Uint16(g[4:]) != 2 {
		t.Fatalf("group header % x", g[:6])
	}
	for i, want := range []int{32, 16} {
		e := g[6+14*i:]
		id := uint32(le.Uint16(e[12:]))
		if int(e[0]) != want || le.Uint32(e[8:]) != uint32(len(res[rtIcon][id])) {
			t.Fatalf("entry %d: size %d, bytes %d vs %d", i, e[0], le.Uint32(e[8:]), len(res[rtIcon][id]))
		}
	}
}
