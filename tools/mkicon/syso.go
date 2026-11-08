package main

import (
	"bytes"
	"encoding/binary"
)

// writeSyso builds a COFF object (amd64) holding an RT_GROUP_ICON (id 1) and its
// RT_ICON images. Go links any *.syso in the package into the executable, so
// Explorer shows the icon for dualnet.exe.
func writeSyso(images [][]byte, sizes []int) []byte {
	le := binary.LittleEndian
	n := len(images)
	type dirEntry struct{ id, off uint32 }
	dir := func(b *bytes.Buffer, entries []dirEntry) {
		binary.Write(b, le, [4]uint32{0, 0, 0, uint32(len(entries)) << 16}) // chars, time, version; named=0 (low), ids=len (high)
		for _, e := range entries {
			binary.Write(b, le, [2]uint32{e.id, e.off})
		}
	}
	const sub = 0x80000000
	dirSize := func(k int) int { return 16 + 8*k }

	// offsets
	root := 0
	iconNames := root + dirSize(2)
	groupNames := iconNames + dirSize(n)
	langs := groupNames + dirSize(1) // n+1 language dirs of 1 entry
	dataEntries := langs + (n+1)*dirSize(1)
	blobs := dataEntries + (n+1)*16
	align := func(x int) int { return (x + 7) &^ 7 }

	// group icon directory blob
	var grp bytes.Buffer
	binary.Write(&grp, le, [3]uint16{0, 1, uint16(n)})
	for i, s := range sizes {
		d := uint8(s)
		if s >= 256 {
			d = 0
		}
		grp.Write([]byte{d, d, 0, 0})
		binary.Write(&grp, le, uint16(1))
		binary.Write(&grp, le, uint16(32))
		binary.Write(&grp, le, uint32(len(images[i])))
		binary.Write(&grp, le, uint16(i+1))
	}
	all := append(append([][]byte{}, images...), grp.Bytes())
	blobOff := make([]int, n+1)
	p := align(blobs)
	for i, b := range all {
		blobOff[i] = p
		p = align(p + len(b))
	}
	total := p

	var raw bytes.Buffer
	dir(&raw, []dirEntry{{3, uint32(iconNames) | sub}, {14, uint32(groupNames) | sub}})
	var e []dirEntry
	for i := 0; i < n; i++ {
		e = append(e, dirEntry{uint32(i + 1), uint32(langs+i*dirSize(1)) | sub})
	}
	dir(&raw, e)
	dir(&raw, []dirEntry{{1, uint32(langs+n*dirSize(1)) | sub}})
	for i := 0; i <= n; i++ {
		dir(&raw, []dirEntry{{0x0409, uint32(dataEntries + i*16)}})
	}
	var relocs []uint32
	for i := 0; i <= n; i++ {
		relocs = append(relocs, uint32(raw.Len()))
		binary.Write(&raw, le, [4]uint32{uint32(blobOff[i]), uint32(len(all[i])), 0, 0})
	}
	for i, b := range all {
		raw.Write(make([]byte, blobOff[i]-raw.Len()))
		raw.Write(b)
	}
	raw.Write(make([]byte, total-raw.Len()))

	var out bytes.Buffer
	rawPtr := 20 + 40
	relocPtr := rawPtr + raw.Len()
	symPtr := relocPtr + 10*len(relocs)
	// file header
	binary.Write(&out, le, struct {
		Machine, NSect          uint16
		Time, SymPtr, NSym      uint32
		OptSize, Characteristic uint16
	}{0x8664, 1, 0, uint32(symPtr), 1, 0, 0})
	// section header
	out.WriteString(".rsrc\x00\x00\x00")
	binary.Write(&out, le, struct {
		VSize, VAddr, RawSize, RawPtr, RelPtr, LinePtr uint32
		NRel, NLine                                    uint16
		Characteristics                                uint32
	}{0, 0, uint32(raw.Len()), uint32(rawPtr), uint32(relocPtr), 0, uint16(len(relocs)), 0, 0x40000040})
	out.Write(raw.Bytes())
	for _, r := range relocs {
		binary.Write(&out, le, r)         // offset in section
		binary.Write(&out, le, uint32(0)) // symbol 0 (.rsrc)
		binary.Write(&out, le, uint16(3)) // IMAGE_REL_AMD64_ADDR32NB
	}
	// symbol table: section symbol
	out.WriteString(".rsrc\x00\x00\x00")
	binary.Write(&out, le, uint32(0)) // value
	binary.Write(&out, le, int16(1))  // section number
	binary.Write(&out, le, uint16(0)) // type
	out.WriteByte(3)                  // IMAGE_SYM_CLASS_STATIC
	out.WriteByte(0)                  // aux
	binary.Write(&out, le, uint32(4)) // empty string table
	return out.Bytes()
}
