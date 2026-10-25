// Package branding supplies the shared executable and tray icon base.
// It uses only Go's standard library; callers may draw badges on Image copies.
package branding

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
)

var iconSizes = [...]int{16, 24, 32, 48, 64, 256}

// Image returns a new antialiased controller icon on a transparent background.
// Sizes from 1 to 256 pixels are supported. The caller owns every pixel.
func Image(size int) *image.NRGBA {
	if size < 1 || size > 256 {
		panic("branding: icon size must be in [1, 256]")
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const samples = 4
	for y := range size {
		for x := range size {
			var red, green, blue, covered int
			for sy := range samples {
				for sx := range samples {
					px := (float64(x) + (float64(sx)+0.5)/samples) / float64(size)
					py := (float64(y) + (float64(sy)+0.5)/samples) / float64(size)
					c := sample(px, py)
					if c.A == 0 {
						continue
					}
					red, green, blue = red+int(c.R), green+int(c.G), blue+int(c.B)
					covered++
				}
			}
			if covered > 0 {
				img.SetNRGBA(x, y, color.NRGBA{R: byte(red / covered), G: byte(green / covered), B: byte(blue / covered), A: byte(covered * 255 / (samples * samples))})
			}
		}
	}
	return img
}

func sample(x, y float64) color.NRGBA {
	if !circle(x, y, 0.5, 0.5, 0.455) {
		return color.NRGBA{}
	}
	teal := color.NRGBA{R: 42, G: 157, B: 143, A: 255}
	if !roundedRect(x, y, 0.23, 0.37, 0.77, 0.60, 0.095) &&
		!roundedRect(x, y, 0.19, 0.47, 0.40, 0.71, 0.09) &&
		!roundedRect(x, y, 0.60, 0.47, 0.81, 0.71, 0.09) {
		return teal
	}
	if (math.Abs(x-0.36) < 0.018 && math.Abs(y-0.485) < 0.074) ||
		(math.Abs(x-0.36) < 0.074 && math.Abs(y-0.485) < 0.018) ||
		circle(x, y, 0.66, 0.435, 0.028) || circle(x, y, 0.715, 0.49, 0.028) {
		return color.NRGBA{R: 20, G: 68, B: 62, A: 255}
	}
	return color.NRGBA{R: 247, G: 250, B: 249, A: 255}
}

func circle(x, y, cx, cy, radius float64) bool {
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= radius*radius
}

func roundedRect(x, y, left, top, right, bottom, radius float64) bool {
	cx := math.Max(left+radius, math.Min(x, right-radius))
	cy := math.Max(top+radius, math.Min(y, bottom-radius))
	return circle(x, y, cx, cy, radius)
}

// ICO encodes the shared base at the sizes used by Explorer and the tray.
// PNG images in ICO files are supported by padmate's Windows 10/11 targets.
func ICO() ([]byte, error) {
	data := make([]byte, 6+16*len(iconSizes))
	binary.LittleEndian.PutUint16(data[2:4], 1)
	binary.LittleEndian.PutUint16(data[4:6], uint16(len(iconSizes)))
	for i, size := range iconSizes {
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, Image(size)); err != nil {
			return nil, fmt.Errorf("branding: encode icon: %w", err)
		}
		entry := data[6+i*16 : 22+i*16]
		if size < 256 {
			entry[0], entry[1] = byte(size), byte(size)
		}
		binary.LittleEndian.PutUint16(entry[4:6], 1)
		binary.LittleEndian.PutUint16(entry[6:8], 32)
		binary.LittleEndian.PutUint32(entry[8:12], uint32(encoded.Len()))
		binary.LittleEndian.PutUint32(entry[12:16], uint32(len(data)))
		data = append(data, encoded.Bytes()...)
	}
	return data, nil
}
