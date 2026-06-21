package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// makeIcon generates a 16x16 solid-circle PNG icon with the given RGB color.
func makeIcon(r, g, b uint8) []byte {
	const size = 16
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	c := color.RGBA{R: r, G: g, B: b, A: 255}
	half := float64(size) / 2
	radius := half - 1
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) - half + 0.5
			dy := float64(y) - half + 0.5
			if dx*dx+dy*dy <= radius*radius {
				img.Set(x, y, c)
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// Icon byte slices as package-level vars so tray.go var declarations can depend on them.
var (
	iconIdleBytes   = makeIcon(120, 120, 120) // gray
	iconActiveBytes = makeIcon(40, 180, 80)   // green
	iconErrorBytes  = makeIcon(210, 60, 60)   // red
)
