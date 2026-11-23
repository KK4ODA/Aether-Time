// SPDX-License-Identifier: GPL-3.0-or-later

// Package winres writes Windows resources (icon, version information and
// application manifest) as COFF object files (.syso). The Go linker picks up
// a .syso placed in a package directory and links its .rsrc section into the
// executable, so Explorer shows the icon and Properties shows the version.
package winres

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"sort"
	"unicode/utf16"

	"github.com/KK4ODA/Aether-Time/internal/icon"
)

// Resource types and language.
const (
	rtIcon      = 3
	rtGroupIcon = 14
	rtVersion   = 16
	rtManifest  = 24
	langEnUS    = 0x0409
)

// Version is the content of the VERSIONINFO resource.
type Version struct {
	File    [4]uint16 // numeric file version
	Product [4]uint16 // numeric product version
	// Strings shown in Properties → Details (CompanyName, FileDescription,
	// FileVersion, ProductName, ProductVersion, LegalCopyright, ...).
	Strings map[string]string
}

// Resources is what gets embedded.
type Resources struct {
	Icons    []*image.NRGBA // one icon group, largest first
	Version  Version
	Manifest string
}

type leaf struct {
	typ, id uint16
	data    []byte
}

// Section lays out the .rsrc section. It returns the bytes and the offsets of
// the data-entry fields that the linker must relocate to image RVAs.
func (r Resources) Section() ([]byte, []uint32) {
	var leaves []leaf
	var group bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&group, le, [3]uint16{0, 1, uint16(len(r.Icons))})
	for i, img := range r.Icons {
		data := icon.IconImage(img)
		leaves = append(leaves, leaf{rtIcon, uint16(i + 1), data})
		dim := icon.Dim(img.Bounds().Dx())
		group.Write([]byte{dim, dim, 0, 0})
		_ = binary.Write(&group, le, [2]uint16{1, 32})
		_ = binary.Write(&group, le, uint32(len(data)))
		_ = binary.Write(&group, le, uint16(i+1))
	}
	if len(r.Icons) > 0 {
		leaves = append(leaves, leaf{rtGroupIcon, 1, group.Bytes()})
	}
	leaves = append(leaves, leaf{rtVersion, 1, r.Version.encode()})
	if r.Manifest != "" {
		leaves = append(leaves, leaf{rtManifest, 1, []byte(r.Manifest)})
	}
	sort.Slice(leaves, func(i, j int) bool {
		if leaves[i].typ != leaves[j].typ {
			return leaves[i].typ < leaves[j].typ
		}
		return leaves[i].id < leaves[j].id
	})

	// Group leaves by type (directory entries must be sorted by ID).
	var types []uint16
	byType := map[uint16][]int{}
	for i, l := range leaves {
		if _, ok := byType[l.typ]; !ok {
			types = append(types, l.typ)
		}
		byType[l.typ] = append(byType[l.typ], i)
	}

	// Offsets: root dir, one dir per type, one language dir per leaf, data
	// entries, then the data itself (8-byte aligned).
	dirSize := func(n int) int { return 16 + 8*n }
	off := dirSize(len(types))
	typeDir := map[uint16]int{}
	for _, t := range types {
		typeDir[t] = off
		off += dirSize(len(byType[t]))
	}
	langDir := make([]int, len(leaves))
	for i := range leaves {
		langDir[i] = off
		off += dirSize(1)
	}
	entry := make([]int, len(leaves))
	for i := range leaves {
		entry[i] = off
		off += 16
	}
	dataOff := make([]int, len(leaves))
	for i, l := range leaves {
		off = align8(off)
		dataOff[i] = off
		off += len(l.data)
	}
	buf := make([]byte, align8(off))

	writeDir := func(at int, ids []uint16, targets []uint32) {
		le.PutUint16(buf[at+14:], uint16(len(ids))) // NumberOfIdEntries
		for i := range ids {
			le.PutUint32(buf[at+16+8*i:], uint32(ids[i]))
			le.PutUint32(buf[at+16+8*i+4:], targets[i])
		}
	}
	const subdir = 0x80000000
	var rootTargets []uint32
	for _, t := range types {
		rootTargets = append(rootTargets, subdir|uint32(typeDir[t]))
	}
	writeDir(0, types, rootTargets)
	for _, t := range types {
		var ids []uint16
		var targets []uint32
		for _, i := range byType[t] {
			ids = append(ids, leaves[i].id)
			targets = append(targets, subdir|uint32(langDir[i]))
		}
		writeDir(typeDir[t], ids, targets)
	}
	var relocs []uint32
	for i, l := range leaves {
		writeDir(langDir[i], []uint16{langEnUS}, []uint32{uint32(entry[i])})
		le.PutUint32(buf[entry[i]:], uint32(dataOff[i])) // OffsetToData: relocated to an RVA
		le.PutUint32(buf[entry[i]+4:], uint32(len(l.data)))
		relocs = append(relocs, uint32(entry[i]))
		copy(buf[dataOff[i]:], l.data)
	}
	return buf, relocs
}

func align8(n int) int { return (n + 7) &^ 7 }

// Object returns a COFF object (.syso) holding the resources for a Go
// architecture: "amd64", "arm64" or "386".
func Object(goarch string, r Resources) ([]byte, error) {
	var machine, relocType uint16
	switch goarch {
	case "amd64":
		machine, relocType = 0x8664, 0x0003 // IMAGE_REL_AMD64_ADDR32NB
	case "arm64":
		machine, relocType = 0xAA64, 0x0002 // IMAGE_REL_ARM64_ADDR32NB
	case "386":
		machine, relocType = 0x014C, 0x0007 // IMAGE_REL_I386_DIR32NB
	default:
		return nil, fmt.Errorf("winres: unsupported architecture %q", goarch)
	}
	sect, relocs := r.Section()
	if len(relocs) > 0xFFFF {
		return nil, fmt.Errorf("winres: too many resources")
	}
	const fileHdr, sectHdr = 20, 40
	rawPtr := uint32(fileHdr + sectHdr)
	relocPtr := rawPtr + uint32(len(sect))
	symPtr := relocPtr + uint32(10*len(relocs))

	var b bytes.Buffer
	le := binary.LittleEndian
	w := func(v any) { _ = binary.Write(&b, le, v) }
	// IMAGE_FILE_HEADER
	w(machine)
	w(uint16(1)) // NumberOfSections
	w(uint32(0)) // TimeDateStamp: reproducible builds
	w(symPtr)    // PointerToSymbolTable
	w(uint32(1)) // NumberOfSymbols
	w(uint16(0)) // SizeOfOptionalHeader
	w(uint16(0)) // Characteristics
	// IMAGE_SECTION_HEADER
	b.WriteString(".rsrc\x00\x00\x00")
	w(uint32(0)) // VirtualSize
	w(uint32(0)) // VirtualAddress
	w(uint32(len(sect)))
	w(rawPtr)
	w(relocPtr)
	w(uint32(0)) // PointerToLinenumbers
	w(uint16(len(relocs)))
	w(uint16(0))          // NumberOfLinenumbers
	w(uint32(0x40000040)) // INITIALIZED_DATA | MEM_READ
	b.Write(sect)
	for _, off := range relocs {
		w(off)       // VirtualAddress (offset in the section)
		w(uint32(0)) // symbol 0: the section itself
		w(relocType)
	}
	// Symbol table: the section symbol the relocations refer to.
	b.WriteString(".rsrc\x00\x00\x00")
	w(uint32(0))   // Value
	w(int16(1))    // SectionNumber
	w(uint16(0))   // Type
	b.WriteByte(3) // StorageClass: STATIC
	b.WriteByte(0) // NumberOfAuxSymbols
	w(uint32(4))   // empty string table
	return b.Bytes(), nil
}

// ---- VERSIONINFO ----

// node is one VS_VERSIONINFO-style block: wLength, wValueLength, wType, key, value, children.
type node struct {
	key      string
	value    []byte
	text     bool // value is a UTF-16 string (wValueLength counts characters)
	children []node
}

func utf16z(s string) []byte {
	u := utf16.Encode([]rune(s + "\x00"))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	return b
}

func pad4(b *bytes.Buffer) {
	for b.Len()%4 != 0 {
		b.WriteByte(0)
	}
}

func (n node) encode() []byte {
	var b bytes.Buffer
	b.Write([]byte{0, 0, 0, 0}) // wLength, wValueLength: filled in below
	typ := uint16(0)
	valueLen := len(n.value)
	if n.text {
		typ = 1
		valueLen = len(n.value) / 2
	}
	_ = binary.Write(&b, binary.LittleEndian, typ)
	b.Write(utf16z(n.key))
	pad4(&b)
	b.Write(n.value)
	for _, c := range n.children {
		pad4(&b)
		b.Write(c.encode())
	}
	out := b.Bytes()
	binary.LittleEndian.PutUint16(out[0:], uint16(len(out)))
	binary.LittleEndian.PutUint16(out[2:], uint16(valueLen))
	return out
}

func (v Version) encode() []byte {
	var fixed bytes.Buffer
	le := binary.LittleEndian
	ms := func(a [4]uint16) (uint32, uint32) {
		return uint32(a[0])<<16 | uint32(a[1]), uint32(a[2])<<16 | uint32(a[3])
	}
	fms, fls := ms(v.File)
	pms, pls := ms(v.Product)
	_ = binary.Write(&fixed, le, []uint32{
		0xFEEF04BD, 0x00010000, // signature, struct version
		fms, fls, pms, pls,
		0x3F, 0, // file flags mask, flags
		0x00040004, // VOS_NT_WINDOWS32
		0x00000001, // VFT_APP
		0, 0, 0,    // subtype, date
	})
	keys := make([]string, 0, len(v.Strings))
	for k := range v.Strings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var strs []node
	for _, k := range keys {
		strs = append(strs, node{key: k, value: utf16z(v.Strings[k]), text: true})
	}
	translation := make([]byte, 4)
	le.PutUint16(translation[0:], langEnUS)
	le.PutUint16(translation[2:], 1200) // Unicode code page
	root := node{
		key:   "VS_VERSION_INFO",
		value: fixed.Bytes(),
		children: []node{
			{key: "StringFileInfo", text: true, children: []node{{key: "040904B0", text: true, children: strs}}},
			{key: "VarFileInfo", text: true, children: []node{{key: "Translation", value: translation}}},
		},
	}
	return root.encode()
}
