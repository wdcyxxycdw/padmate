package main

import (
	"bytes"
	"encoding/binary"
)

// iconSize is the width and height of the tray icon, in pixels.
const iconSize = 16

// The parts of a .ico file with one 32-bit image, as binary.Write lays
// them out: little-endian, without padding.
type (
	iconDir struct {
		Reserved, Type, Count uint16
	}
	iconDirEntry struct {
		Width, Height, ColorCount, Reserved uint8
		Planes, BitCount                    uint16
		BytesInRes, ImageOffset             uint32
	}
	bitmapInfoHeader struct {
		Size                         uint32
		Width, Height                int32
		Planes, BitCount             uint16
		Compression, SizeImage       uint32
		XPelsPerMeter, YPelsPerMeter int32
		ClrUsed, ClrImportant        uint32
	}
)

// The sizes of the parts, in bytes.
const (
	iconHeaderSize = 6 + 16 // iconDir and one iconDirEntry
	iconInfoSize   = 40     // bitmapInfoHeader
	iconXORSize    = iconSize * iconSize * 4
	iconANDSize    = (iconSize + 31) / 32 * 4 * iconSize // 1 bit per pixel, rows padded to 32 bits
	iconImageSize  = iconInfoSize + iconXORSize + iconANDSize
)

// iconColor is the circle's color, as the BGR bytes of a 32-bit DIB.
var iconColor = [3]byte{0x8f, 0x9d, 0x2a}

// trayIcon is the tray icon, generated so that the repository holds no
// binary asset.
var trayIcon = makeIcon()

// makeIcon returns a .ico file with one 16x16 32-bit image: an
// antialiased filled circle on a transparent background.
func makeIcon() []byte {
	var b bytes.Buffer
	for _, part := range []any{
		iconDir{Type: 1, Count: 1},
		iconDirEntry{
			Width: iconSize, Height: iconSize, Planes: 1, BitCount: 32,
			BytesInRes: iconImageSize, ImageOffset: iconHeaderSize,
		},
		// The height counts both masks.
		bitmapInfoHeader{
			Size: iconInfoSize, Width: iconSize, Height: 2 * iconSize, Planes: 1, BitCount: 32,
			SizeImage: iconXORSize + iconANDSize,
		},
	} {
		_ = binary.Write(&b, binary.LittleEndian, part) // a bytes.Buffer does not fail
	}
	// The XOR mask: BGRA pixels with straight alpha, bottom row first.
	for y := iconSize - 1; y >= 0; y-- {
		for x := range iconSize {
			b.Write(iconColor[:])
			b.WriteByte(iconCoverage(x, y))
		}
	}
	// The AND mask stays 0: the alpha channel decides.
	b.Write(make([]byte, iconANDSize))
	return b.Bytes()
}

// iconCoverage returns how much of pixel (x, y), counted from the top
// left, the circle covers, as an alpha value, by sampling it 4x4 times.
func iconCoverage(x, y int) byte {
	const (
		n      = 4
		center = iconSize / 2.0
		radius = iconSize/2.0 - 1
	)
	in := 0
	for i := range n {
		for j := range n {
			dx := float64(x) + (float64(i)+0.5)/n - center
			dy := float64(y) + (float64(j)+0.5)/n - center
			if dx*dx+dy*dy <= radius*radius {
				in++
			}
		}
	}
	return byte(in * 255 / (n * n))
}
