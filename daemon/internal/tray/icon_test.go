package tray

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"testing"
)

func TestTrayIconsDecodeAndDistinguishModes(t *testing.T) {
	seen := make(map[string]bool)
	for _, inactive := range []bool{false, true} {
		for badge := 0; badge <= 4; badge++ {
			ico, err := Icon(badge, inactive)
			if err != nil {
				t.Fatal(err)
			}
			if seen[string(ico)] {
				t.Fatalf("identical icon for badge %d inactive %t", badge, inactive)
			}
			seen[string(ico)] = true
			count := int(binary.LittleEndian.Uint16(ico[4:6]))
			for i := range count {
				e := ico[6+i*16 : 22+i*16]
				length, offset := binary.LittleEndian.Uint32(e[8:12]), binary.LittleEndian.Uint32(e[12:16])
				if uint64(offset)+uint64(length) > uint64(len(ico)) {
					t.Fatal("ICO payload out of bounds")
				}
				img, err := png.Decode(bytes.NewReader(ico[offset : offset+length]))
				if err != nil || img.Bounds().Dx() != int(e[0]) || img.Bounds().Dy() != int(e[1]) {
					t.Fatalf("invalid ICO image: %v", err)
				}
			}
		}
	}
}
