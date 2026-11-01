package jc2

import (
	"bytes"
	"testing"
)

// infoBlock builds a FlashInfo block with an invented serial number.
func infoBlock(serial string, vendor, product uint16) []byte {
	b := make([]byte, FlashInfo.Len)
	copy(b[2:16], serial)
	b[0x12], b[0x13] = byte(vendor), byte(vendor>>8)
	b[0x14], b[0x15] = byte(product), byte(product>>8)
	return b
}

func TestParseInfo(t *testing.T) {
	tests := []struct {
		name  string
		block []byte
		want  Info
		ok    bool
	}{
		{"padded", infoBlock("TESTSER0000001", VendorID, ProductR), Info{"TESTSER0000001", VendorID, ProductR}, true},
		{"full 14", infoBlock("ABCDEFGHIJKLMN", VendorID, ProductL), Info{"ABCDEFGHIJKLMN", VendorID, ProductL}, true},
		{"short serial", infoBlock("AB", VendorID, ProductL), Info{"AB", VendorID, ProductL}, true},
		{"minimal block", infoBlock("AB", VendorID, ProductL)[:0x16], Info{"AB", VendorID, ProductL}, true},
		{"too short", infoBlock("AB", VendorID, ProductL)[:0x15], Info{}, false},
		{"empty serial", infoBlock("", VendorID, ProductL), Info{}, false},
		{"blank flash", bytes.Repeat([]byte{0xff}, 0x40), Info{}, false},
		{"control character", infoBlock("AB\x01CD", VendorID, ProductL), Info{}, false},
		{"NUL inside", infoBlock("AB\x00CD", VendorID, ProductL), Info{}, false},
		{"non-ASCII", infoBlock("AB\xc3\xa9", VendorID, ProductL), Info{}, false},
		{"space is printable", infoBlock("AB CD", VendorID, ProductL), Info{"AB CD", VendorID, ProductL}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseInfo(tt.block)
			if (err == nil) != tt.ok || got != tt.want {
				t.Errorf("ParseInfo = %+v, %v; want %+v, ok %v", got, err, tt.want, tt.ok)
			}
		})
	}
}
