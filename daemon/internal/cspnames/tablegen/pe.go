package tablegen

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
)

// image is a PE file mapped for static reading.
type image struct {
	data []byte
	base uint64
	secs []*pe.Section
}

func openImage(data []byte) (*image, error) {
	f, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("tablegen: not a PE file: %w", err)
	}
	oh, ok := f.OptionalHeader.(*pe.OptionalHeader64)
	if !ok {
		return nil, errors.New("tablegen: not a 64-bit PE file")
	}
	return &image{data: data, base: oh.ImageBase, secs: f.Sections}, nil
}

// section returns a section by name.
func (im *image) section(name string) *pe.Section {
	for _, s := range im.secs {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// bytesOf returns the file bytes of a section.
func (im *image) bytesOf(s *pe.Section) ([]byte, error) {
	end := uint64(s.Offset) + uint64(s.Size)
	if end > uint64(len(im.data)) {
		return nil, fmt.Errorf("tablegen: section %s is outside the file", s.Name)
	}
	return im.data[s.Offset:end], nil
}

// offset converts a virtual address to a file offset; ok is false when no
// section maps it.
func (im *image) offset(va uint64) (int, bool) {
	if va < im.base {
		return 0, false
	}
	r := va - im.base
	for _, s := range im.secs {
		sva := uint64(s.VirtualAddress)
		span := uint64(max(s.VirtualSize, s.Size))
		if r >= sva && r < sva+span && r-sva < uint64(s.Size) {
			off := uint64(s.Offset) + r - sva
			if off < uint64(len(im.data)) {
				return int(off), true
			}
		}
	}
	return 0, false
}

// cstr reads the NUL-terminated string at va.
func (im *image) cstr(va uint64) string {
	o, ok := im.offset(va)
	if !ok {
		return ""
	}
	end := bytes.IndexByte(im.data[o:min(len(im.data), o+512)], 0)
	if end < 0 {
		return ""
	}
	return string(im.data[o : o+end])
}

func le32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }
func rel32(b []byte) int64 { return int64(int32(binary.LittleEndian.Uint32(b))) }
