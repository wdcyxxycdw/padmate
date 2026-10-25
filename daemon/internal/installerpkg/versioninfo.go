package installerpkg

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"unicode/utf16"

	"github.com/tc-hib/winres"
)

// VersionInfo is the version resource (VS_VERSIONINFO) of a Windows
// executable: the numeric versions of its fixed part and the strings of the
// first language's table.
type VersionInfo struct {
	// File and Product are the numeric file and product versions.
	File, Product [4]uint16
	// Strings is the string table of the lowest language the resource has:
	// FileVersion, ProductVersion and so on.
	Strings map[string]string
}

// ReadVersionInfo reads the version resource of a PE file. It parses the
// resource itself rather than through a library: NSIS labels its tables with
// a code page that library refuses, though the strings are UTF-16 all the
// same.
func ReadVersionInfo(exe io.ReadSeeker) (VersionInfo, error) {
	resources, err := winres.LoadFromEXE(exe)
	if err != nil {
		return VersionInfo{}, fmt.Errorf("resources: %w", err)
	}
	var data []byte
	resources.WalkType(winres.RT_VERSION, func(_ winres.Identifier, _ uint16, d []byte) bool {
		data = d
		return false
	})
	if data == nil {
		return VersionInfo{}, errors.New("no version resource")
	}
	return parseVersionInfo(data)
}

// versionNode is one node of the tree a version resource is: a key, a value
// and children, each node starting on a 4-byte boundary.
type versionNode struct {
	key      string
	value    []byte
	text     bool
	children []byte
}

// readVersionNode reads the node at the start of b and returns it with the
// number of bytes it occupies, padding included.
func readVersionNode(b []byte) (versionNode, int, error) {
	if len(b) < 6 {
		return versionNode{}, 0, errors.New("version resource: truncated node")
	}
	length := int(binary.LittleEndian.Uint16(b[0:]))
	valueLength := int(binary.LittleEndian.Uint16(b[2:]))
	text := binary.LittleEndian.Uint16(b[4:]) == 1
	if length < 6 || length > len(b) {
		return versionNode{}, 0, errors.New("version resource: bad node length")
	}
	var key []uint16
	pos := 6
	for {
		if pos+2 > length {
			return versionNode{}, 0, errors.New("version resource: unterminated key")
		}
		c := binary.LittleEndian.Uint16(b[pos:])
		pos += 2
		if c == 0 {
			break
		}
		key = append(key, c)
	}
	pos = (pos + 3) &^ 3
	size := valueLength
	if text {
		size *= 2
	}
	if pos+size > length {
		return versionNode{}, 0, errors.New("version resource: value outside its node")
	}
	n := versionNode{key: string(utf16.Decode(key)), value: b[pos : pos+size], text: text}
	pos = (pos + size + 3) &^ 3
	if pos < length {
		n.children = b[pos:length]
	}
	return n, (length + 3) &^ 3, nil
}

func (n versionNode) each(f func(versionNode)) error {
	for rest := n.children; len(rest) >= 6; {
		child, size, err := readVersionNode(rest)
		if err != nil {
			return err
		}
		f(child)
		if size >= len(rest) {
			break
		}
		rest = rest[size:]
	}
	return nil
}

func parseVersionInfo(data []byte) (VersionInfo, error) {
	root, _, err := readVersionNode(data)
	if err != nil {
		return VersionInfo{}, err
	}
	if root.key != "VS_VERSION_INFO" {
		return VersionInfo{}, fmt.Errorf("version resource: first node is %q", root.key)
	}
	info := VersionInfo{Strings: map[string]string{}}
	if len(root.value) >= 24 && binary.LittleEndian.Uint32(root.value) == 0xFEEF04BD {
		split := func(ms, ls uint32) [4]uint16 {
			return [4]uint16{uint16(ms >> 16), uint16(ms), uint16(ls >> 16), uint16(ls)}
		}
		word := func(i int) uint32 { return binary.LittleEndian.Uint32(root.value[i:]) }
		info.File, info.Product = split(word(8), word(12)), split(word(16), word(20))
	}
	var tables []versionNode
	err = root.each(func(n versionNode) {
		if n.key != "StringFileInfo" {
			return
		}
		_ = n.each(func(table versionNode) { tables = append(tables, table) })
	})
	if err != nil {
		return VersionInfo{}, err
	}
	// The table of the lowest language, by its key ("040904B0"), so the choice
	// does not depend on order.
	var first *versionNode
	for i := range tables {
		if first == nil || tables[i].key < first.key {
			first = &tables[i]
		}
	}
	if first == nil {
		return info, nil
	}
	err = first.each(func(s versionNode) {
		if !s.text {
			return
		}
		u := make([]uint16, len(s.value)/2)
		for i := range u {
			u[i] = binary.LittleEndian.Uint16(s.value[2*i:])
		}
		for len(u) > 0 && u[len(u)-1] == 0 {
			u = u[:len(u)-1]
		}
		info.Strings[s.key] = string(utf16.Decode(u))
	})
	return info, err
}
