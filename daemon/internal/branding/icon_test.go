package branding

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"testing"
)

func TestImageReturnsIndependentAntialiasedBase(t *testing.T) {
	for _, size := range []int{16, 24, 32, 48, 64, 256} {
		img := Image(size)
		if img.Bounds().Dx() != size || img.Bounds().Dy() != size {
			t.Fatalf("size %d: bounds %v", size, img.Bounds())
		}
		if img.NRGBAAt(0, 0).A != 0 || img.NRGBAAt(size/2, size/2).A != 255 {
			t.Fatalf("size %d: icon needs transparent corners and an opaque center", size)
		}
		original := Image(size)
		img.Pix[(size/2)*img.Stride+(size/2)*4] ^= 0xff
		if bytes.Equal(img.Pix, original.Pix) {
			t.Fatal("image copies did not change independently")
		}
		if !bytes.Equal(original.Pix, Image(size).Pix) {
			t.Fatal("mutating a badge base changed future icons")
		}
	}
}

func TestICOContainsEveryExplorerAndTraySize(t *testing.T) {
	data, err := ICO()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 6 || binary.LittleEndian.Uint16(data[2:4]) != 1 || binary.LittleEndian.Uint16(data[4:6]) != 6 {
		t.Fatal("invalid ICO directory")
	}
	for i, size := range []int{16, 24, 32, 48, 64, 256} {
		entry := data[6+i*16 : 22+i*16]
		length, offset := binary.LittleEndian.Uint32(entry[8:12]), binary.LittleEndian.Uint32(entry[12:16])
		if uint64(offset)+uint64(length) > uint64(len(data)) {
			t.Fatal("ICO image outside file")
		}
		img, err := png.Decode(bytes.NewReader(data[offset : offset+length]))
		if err != nil || img.Bounds().Dx() != size || img.Bounds().Dy() != size {
			t.Fatalf("size %d: image=%v error=%v", size, img, err)
		}
	}
}
