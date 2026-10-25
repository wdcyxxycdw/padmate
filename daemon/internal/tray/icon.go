package tray

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"image/png"
	"sync"

	"github.com/wdcyxxycdw/padmate/daemon/internal/branding"
)

var cachedIcons = sync.OnceValues(func() ([2][5][]byte, error) {
	var icons [2][5][]byte
	for state := range 2 {
		for badge := range 5 {
			data, err := makeIcon(badge, state == 1)
			if err != nil {
				return icons, err
			}
			icons[state][badge] = data
		}
	}
	return icons, nil
})

// Icon returns a cached ICO. The caller must not modify its bytes.
func Icon(badge int, inactive bool) ([]byte, error) {
	icons, err := cachedIcons()
	if badge < 0 || badge > 4 {
		badge = 0
	}
	state := 0
	if inactive {
		state = 1
	}
	return icons[state][badge], err
}

func makeIcon(badge int, inactive bool) ([]byte, error) {
	sizes := []int{16, 24, 32, 48, 64}
	out := make([]byte, 6+16*len(sizes))
	binary.LittleEndian.PutUint16(out[2:4], 1)
	binary.LittleEndian.PutUint16(out[4:6], uint16(len(sizes)))
	digits := [5][5]string{{}, {"010", "110", "010", "010", "111"}, {"110", "001", "010", "100", "111"}, {"110", "001", "010", "001", "110"}, {"101", "101", "111", "001", "001"}}
	for index, size := range sizes {
		img := branding.Image(size)
		if inactive {
			for y := range size {
				for x := range size {
					c := img.NRGBAAt(x, y)
					g := byte((int(c.R) + int(c.G) + int(c.B)) / 3)
					img.SetNRGBA(x, y, color.NRGBA{R: g, G: g, B: g, A: c.A})
				}
			}
		}
		if badge > 0 {
			left, top, width := size*9/16, size*8/16, size*7/16
			for y := top; y < size; y++ {
				for x := left; x < size; x++ {
					img.SetNRGBA(x, y, color.NRGBA{R: 20, G: 45, B: 70, A: 255})
				}
			}
			for y, row := range digits[badge] {
				for x, bit := range row {
					if bit != '1' {
						continue
					}
					for py := top + (y+1)*(size-top)/7; py < top+(y+2)*(size-top)/7; py++ {
						for px := left + (x+1)*width/5; px < left+(x+2)*width/5; px++ {
							img.SetNRGBA(px, py, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
						}
					}
				}
			}
		}
		var pngBytes bytes.Buffer
		if err := png.Encode(&pngBytes, img); err != nil {
			return nil, err
		}
		entry := out[6+16*index : 6+16*(index+1)]
		entry[0], entry[1] = byte(size), byte(size)
		binary.LittleEndian.PutUint16(entry[4:6], 1)
		binary.LittleEndian.PutUint16(entry[6:8], 32)
		binary.LittleEndian.PutUint32(entry[8:12], uint32(pngBytes.Len()))
		binary.LittleEndian.PutUint32(entry[12:16], uint32(len(out)))
		out = append(out, pngBytes.Bytes()...)
	}
	return out, nil
}
