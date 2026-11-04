package cspnames

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"unicode/utf16"
)

const (
	rtString        = 6
	maxPEEntries    = 4096
	maxStringBlocks = 4096
)

// PEStrings holds the RT_STRING resources of a PE file: language id to
// string id to text.
type PEStrings map[uint16]map[uint32]string

// ReadPEStrings reads the string table resources of a PE file (a plug-in
// DLL). It never panics; a file without resources yields an empty result.
func ReadPEStrings(data []byte) (out PEStrings, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("cspnames: reading PE resources: %v", r)
		}
	}()
	f, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var dir pe.DataDirectory
	switch oh := f.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		if oh.NumberOfRvaAndSizes > pe.IMAGE_DIRECTORY_ENTRY_RESOURCE {
			dir = oh.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_RESOURCE]
		}
	case *pe.OptionalHeader64:
		if oh.NumberOfRvaAndSizes > pe.IMAGE_DIRECTORY_ENTRY_RESOURCE {
			dir = oh.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_RESOURCE]
		}
	}
	out = PEStrings{}
	if dir.VirtualAddress == 0 || dir.Size == 0 {
		return out, nil
	}
	// rva returns n bytes of the image at the given RVA, from the file data.
	rva := func(addr, n uint32) []byte {
		for _, s := range f.Sections {
			if addr >= s.VirtualAddress && uint64(addr)+uint64(n) <= uint64(s.VirtualAddress)+uint64(s.Size) {
				start := uint64(s.Offset) + uint64(addr-s.VirtualAddress)
				if start+uint64(n) <= uint64(len(data)) {
					return data[start : start+uint64(n)]
				}
			}
		}
		return nil
	}
	root := dir.VirtualAddress
	type entry struct {
		id     uint32
		offset uint32
		named  bool
	}
	// readDir returns the entries of the resource directory at the offset
	// from the start of the resource section.
	readEntries := func(off uint32) []entry {
		h := rva(root+off, 16)
		if h == nil {
			return nil
		}
		n := int(binary.LittleEndian.Uint16(h[12:])) + int(binary.LittleEndian.Uint16(h[14:]))
		if n > maxPEEntries {
			return nil
		}
		raw := rva(root+off+16, uint32(8*n))
		if raw == nil && n > 0 {
			return nil
		}
		es := make([]entry, n)
		for i := range es {
			name := binary.LittleEndian.Uint32(raw[8*i:])
			es[i] = entry{id: name & 0xFFFF, offset: binary.LittleEndian.Uint32(raw[8*i+4:]), named: name&0x80000000 != 0}
		}
		return es
	}
	blocks := 0
	for _, t := range readEntries(0) {
		if t.named || t.id != rtString || t.offset&0x80000000 == 0 {
			continue
		}
		for _, b := range readEntries(t.offset & 0x7FFFFFFF) {
			if b.named || b.offset&0x80000000 == 0 {
				continue
			}
			for _, l := range readEntries(b.offset & 0x7FFFFFFF) {
				if l.named || l.offset&0x80000000 != 0 {
					continue
				}
				de := rva(root+l.offset, 8)
				if de == nil {
					continue
				}
				blocks++
				if blocks > maxStringBlocks {
					return out, nil
				}
				body := rva(binary.LittleEndian.Uint32(de), binary.LittleEndian.Uint32(de[4:]))
				readStringBlock(out, uint16(l.id), b.id, body)
			}
		}
	}
	return out, nil
}

// readStringBlock reads the 16 length-prefixed UTF-16 strings of one
// RT_STRING block; block b holds string ids (b-1)*16 to (b-1)*16+15.
func readStringBlock(out PEStrings, lang uint16, block uint32, body []byte) {
	if block == 0 {
		return
	}
	pos := 0
	for n := range uint32(16) {
		if pos+2 > len(body) {
			return
		}
		l := int(binary.LittleEndian.Uint16(body[pos:]))
		pos += 2
		if l == 0 {
			continue
		}
		if pos+2*l > len(body) {
			return
		}
		u := make([]uint16, l)
		for i := range u {
			u[i] = binary.LittleEndian.Uint16(body[pos+2*i:])
		}
		pos += 2 * l
		if out[lang] == nil {
			out[lang] = map[uint32]string{}
		}
		out[lang][(block-1)*16+n] = string(utf16.Decode(u))
	}
}

// errNoPluginName is returned when a plug-in has no usable name string.
var errNoPluginName = errors.New("cspnames: plug-in has no name string")

// pluginName picks the filter name of a plug-in from its string table. The
// plug-in's own strings start with its category ("Effect") at the lowest
// string id and the filter name at the second lowest. The language ids are
// tried in order; strings with a "|" are file type descriptions of the import
// and export plug-ins, not names. It returns the name and the category.
func (s PEStrings) pluginName(langs []uint16) (name, category string, err error) {
	ids := map[uint32]bool{}
	for _, m := range s {
		for id := range m {
			ids[id] = true
		}
	}
	sorted := make([]uint32, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	if len(sorted) < 2 {
		return "", "", errNoPluginName
	}
	pick := func(id uint32) string {
		for _, l := range langs {
			if v := s[l][id]; v != "" {
				return v
			}
		}
		return ""
	}
	name = pick(sorted[1])
	if name == "" || bytes.ContainsRune([]byte(name), '|') {
		return "", "", errNoPluginName
	}
	return name, pick(sorted[0]), nil
}
