package thumbs

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Exercises the real shell API on a local JPEG.
func TestShellGenerateThenCached(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	for y := range 480 {
		for x := range 640 {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	// A unique name and mtime so an earlier run's cache entry cannot match.
	path := filepath.Join(t.TempDir(), "thumb-"+time.Now().Format("150405.000000000")+".jpg")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(f, img, nil); err != nil {
		t.Fatal(err)
	}
	f.Close()

	withCOM(func() {
		if shellHas(path, 256) {
			t.Fatal("brand-new file reported as cached")
		}
		if err := shellGenerate(path, 256); err != nil {
			t.Fatalf("generate: %v", err)
		}
		if !shellHas(path, 256) {
			t.Fatal("thumbnail not in the cache after generating")
		}
		if !shellHas(path, 96) {
			t.Fatal("a 256px thumbnail should also serve 96px")
		}
	})
	if s := DefaultSize(); s < 256 || s > 1024 {
		t.Fatalf("DefaultSize = %d", s)
	}
}
