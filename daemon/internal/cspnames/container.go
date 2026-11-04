package cspnames

import (
	"bytes"
	"encoding/binary"
)

// Limits that keep a malformed resource from costing more than it is worth.
const (
	// maxResourceSize bounds the main resource file (the real one is 3.5 MB).
	maxResourceSize = 64 << 20
	// maxDirEntries bounds one directory of the container.
	maxDirEntries = 20000
	// maxSections bounds the top-level section table.
	maxSections = 64
	// maxMenuDepth bounds the nesting of menus.
	maxMenuDepth = 32
)

// section is one entry of the top-level table of the container.
type section struct {
	id, off, size uint32
}

// dirEntry is one entry of a directory: an id and the absolute file range of
// its content. Entries are contiguous and the first starts right after the
// table, which is what tells a directory from other data.
type dirEntry struct {
	id, off, size uint32
}

// be32 reads a big-endian u32 at p.
func be32(d []byte, p int) (uint32, bool) {
	if p < 0 || p+4 > len(d) {
		return 0, false
	}
	return binary.BigEndian.Uint32(d[p:]), true
}

// be16 reads a big-endian u16 at p.
func be16(d []byte, p int) (uint16, bool) {
	if p < 0 || p+2 > len(d) {
		return 0, false
	}
	return binary.BigEndian.Uint16(d[p:]), true
}

// readSections reads the section table at the start of the file: u32 count,
// then count entries of (id, offset, size).
func readSections(d []byte) ([]section, bool) {
	n, ok := be32(d, 0)
	if !ok || n < 2 || n > maxSections || 4+12*int(n) > len(d) {
		return nil, false
	}
	secs := make([]section, n)
	for i := range secs {
		p := 4 + 12*i
		secs[i].id = binary.BigEndian.Uint32(d[p:])
		secs[i].off = binary.BigEndian.Uint32(d[p+4:])
		secs[i].size = binary.BigEndian.Uint32(d[p+8:])
		if uint64(secs[i].off)+uint64(secs[i].size) > uint64(len(d)) {
			return nil, false
		}
	}
	return secs, true
}

// readDir reads the directory at p: u32 N, then N entries of (id, absolute
// offset, size) whose contents follow one another, the first right after the
// table. It returns nil when p is not a directory.
func readDir(d []byte, p int) []dirEntry {
	if p < 0 || p+16 > len(d) {
		return nil
	}
	n32, _ := be32(d, p)
	if n32 < 1 || n32 > maxDirEntries {
		return nil
	}
	n := int(n32)
	end := p + 4 + 12*n
	if end > len(d) {
		return nil
	}
	if first, _ := be32(d, p+8); uint64(first) != uint64(end) {
		return nil // cheap rejection: most offsets are not directories
	}
	ents := make([]dirEntry, n)
	exp := uint64(end)
	for i := range ents {
		q := p + 4 + 12*i
		e := dirEntry{
			id:   binary.BigEndian.Uint32(d[q:]),
			off:  binary.BigEndian.Uint32(d[q+4:]),
			size: binary.BigEndian.Uint32(d[q+8:]),
		}
		if uint64(e.off) != exp {
			return nil
		}
		exp += uint64(e.size)
		if exp > uint64(len(d)) {
			return nil
		}
		ents[i] = e
	}
	return ents
}

// content returns the bytes of e.
func (e dirEntry) content(d []byte) []byte {
	return d[e.off : e.off+e.size]
}

// lengthPrefixed reads a u32-length-prefixed string at the start of b.
func lengthPrefixed(b []byte) (string, bool) {
	n, ok := be32(b, 0)
	if !ok || uint64(n) > uint64(len(b)-4) {
		return "", false
	}
	return toValidUTF8(b[4 : 4+n]), true
}

// toValidUTF8 returns b as a string with invalid UTF-8 replaced.
func toValidUTF8(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	r := bytes.ToValidUTF8(b, []byte("�"))
	return string(r)
}

// indexFrom returns the index of sep in b at or after from, or -1.
func indexFrom(b, sep []byte, from int) int {
	if from < 0 || from > len(b) {
		return -1
	}
	i := bytes.Index(b[from:], sep)
	if i < 0 {
		return -1
	}
	return from + i
}
