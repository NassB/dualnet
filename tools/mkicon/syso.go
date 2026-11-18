package main

import (
	"bytes"
	"encoding/binary"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Resource types (winuser.h).
const (
	rtIcon      = 3
	rtGroupIcon = 14
	rtVersion   = 16
	rtManifest  = 24
)

type resource struct {
	typ, id uint32
	data    []byte
}

// exeResources is everything linked into dualnet.exe: the icon group (id 1, used
// by the tray), version information (Explorer's Details tab) and a manifest. The
// version information and manifest also make the .exe look less anonymous to
// antivirus heuristics.
func exeResources(images [][]byte, sizes []int, version, build string) []resource {
	var res []resource
	var grp bytes.Buffer
	le := binary.LittleEndian
	binary.Write(&grp, le, [3]uint16{0, 1, uint16(len(images))})
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
		res = append(res, resource{rtIcon, uint32(i + 1), images[i]})
	}
	res = append(res,
		resource{rtGroupIcon, 1, grp.Bytes()},
		resource{rtVersion, 1, versionInfo(version, build)},
		resource{rtManifest, 1, []byte(manifest)},
	)
	return res
}

// manifest: run as the invoking user (dualnet asks for elevation itself, via
// "runas", only when it needs it) and declare Windows 10/11 support.
const manifest = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity type="win32" name="NassB.dualnet" version="1.0.0.0" processorArchitecture="amd64"/>
  <description>dualnet</description>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application>
      <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
    </application>
  </compatibility>
</assembly>
`

// versionNumbers turns "v0.11.0" and build "64" into 0, 11, 0, 64. Anything
// that is not a vX.Y.Z version (a dev build) gives 0.0.0.
func versionNumbers(version, build string) [4]uint16 {
	var v [4]uint16
	parts := strings.SplitN(strings.TrimPrefix(version, "v"), ".", 3)
	if len(parts) == 3 {
		for i, p := range parts {
			n, err := strconv.ParseUint(p, 10, 16)
			if err != nil {
				return [4]uint16{}
			}
			v[i] = uint16(n)
		}
	}
	if n, err := strconv.ParseUint(build, 10, 16); err == nil {
		v[3] = uint16(n)
	}
	return v
}

// versionInfo builds a VS_VERSIONINFO block (English, Unicode).
func versionInfo(version, build string) []byte {
	v := versionNumbers(version, build)
	ms := uint32(v[0])<<16 | uint32(v[1])
	ls := uint32(v[2])<<16 | uint32(v[3])
	var fixed bytes.Buffer
	binary.Write(&fixed, binary.LittleEndian, [13]uint32{
		0xFEEF04BD, 0x00010000, // signature, struct version
		ms, ls, ms, ls, // file and product version
		0x3F, 0, // flags mask, flags
		0x00040004, 1, 0, // VOS_NT_WINDOWS32, VFT_APP, no subtype
		0, 0, // date
	})
	shown := version
	if build != "" {
		shown += " (build " + build + ")"
	}
	strs := [][2]string{
		{"CompanyName", "NassB"},
		{"FileDescription", "dualnet - combine several internet connections"},
		{"FileVersion", shown},
		{"InternalName", "dualnet"},
		{"LegalCopyright", "Copyright (c) 2026 NassB. MIT License."},
		{"OriginalFilename", "dualnet.exe"},
		{"ProductName", "dualnet"},
		{"ProductVersion", shown},
	}
	var table [][]byte
	for _, s := range strs {
		val := utf16z(s[1])
		table = append(table, verNode(s[0], val, 1, len(val)/2))
	}
	return verNode("VS_VERSION_INFO", fixed.Bytes(), 0, fixed.Len(),
		verNode("StringFileInfo", nil, 1, 0, verNode("040904B0", nil, 1, 0, table...)),
		verNode("VarFileInfo", nil, 1, 0, verNode("Translation", []byte{0x09, 0x04, 0xB0, 0x04}, 0, 4)),
	)
}

func utf16z(s string) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, append(utf16.Encode([]rune(s)), 0))
	return b.Bytes()
}

// verNode is one node of a version resource: wLength, wValueLength, wType, the
// key, the value and the children, each aligned on 4 bytes.
func verNode(key string, value []byte, typ, valueLen int, children ...[]byte) []byte {
	var b bytes.Buffer
	b.Write(make([]byte, 6))
	b.Write(utf16z(key))
	pad := func() { b.Write(make([]byte, (4-b.Len()%4)%4)) }
	pad()
	b.Write(value)
	for _, c := range children {
		pad()
		b.Write(c)
	}
	out := b.Bytes()
	binary.LittleEndian.PutUint16(out[0:], uint16(len(out)))
	binary.LittleEndian.PutUint16(out[2:], uint16(valueLen))
	binary.LittleEndian.PutUint16(out[4:], uint16(typ))
	return out
}

// writeSyso builds a COFF object (amd64) with a .rsrc section holding res
// (language en-US). Go links any *.syso in the package into the executable.
func writeSyso(res []resource) []byte {
	le := binary.LittleEndian
	sort.Slice(res, func(i, j int) bool {
		if res[i].typ != res[j].typ {
			return res[i].typ < res[j].typ
		}
		return res[i].id < res[j].id
	})
	type dirEntry struct{ id, off uint32 }
	dir := func(b *bytes.Buffer, entries []dirEntry) {
		binary.Write(b, le, [4]uint32{0, 0, 0, uint32(len(entries)) << 16}) // chars, time, version; named=0 (low), ids=len (high)
		for _, e := range entries {
			binary.Write(b, le, [2]uint32{e.id, e.off})
		}
	}
	const sub = 0x80000000
	dirSize := func(k int) int { return 16 + 8*k }
	align := func(x int) int { return (x + 7) &^ 7 }

	// Group resources by type, in order.
	var types []uint32
	byType := map[uint32][]int{}
	for i, r := range res {
		if len(byType[r.typ]) == 0 {
			types = append(types, r.typ)
		}
		byType[r.typ] = append(byType[r.typ], i)
	}
	n := len(res)

	// Layout: root dir, one name dir per type, one language dir per resource,
	// one data entry per resource, then the data.
	nameDir := map[uint32]int{}
	p := dirSize(len(types))
	for _, t := range types {
		nameDir[t] = p
		p += dirSize(len(byType[t]))
	}
	langs := p
	dataEntries := langs + n*dirSize(1)
	blobOff := make([]int, n)
	p = align(dataEntries + n*16)
	for i, r := range res {
		blobOff[i] = p
		p = align(p + len(r.data))
	}
	total := p

	var raw bytes.Buffer
	var e []dirEntry
	for _, t := range types {
		e = append(e, dirEntry{t, uint32(nameDir[t]) | sub})
	}
	dir(&raw, e)
	for _, t := range types {
		e = e[:0]
		for _, i := range byType[t] {
			e = append(e, dirEntry{res[i].id, uint32(langs+i*dirSize(1)) | sub})
		}
		dir(&raw, e)
	}
	for i := range res {
		dir(&raw, []dirEntry{{0x0409, uint32(dataEntries + i*16)}})
	}
	var relocs []uint32
	for i, r := range res {
		relocs = append(relocs, uint32(raw.Len()))
		binary.Write(&raw, le, [4]uint32{uint32(blobOff[i]), uint32(len(r.data)), 0, 0})
	}
	for i, r := range res {
		raw.Write(make([]byte, blobOff[i]-raw.Len()))
		raw.Write(r.data)
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
