package termimg

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestPrepareImage_PNGPassthrough(t *testing.T) {
	// Create a small PNG.
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	data, mime := PrepareImage(buf.Bytes(), "image/png")
	if mime != "image/png" {
		t.Fatalf("mime changed: %s", mime)
	}
	// Small PNG should pass through unchanged.
	if !bytes.Equal(data, buf.Bytes()) {
		t.Fatal("small PNG should pass through unchanged")
	}
}

func TestPrepareImage_EmptyInput(t *testing.T) {
	data, mime := PrepareImage(nil, "image/png")
	if data != nil {
		t.Fatal("nil input should return nil")
	}
	if mime != "image/png" {
		t.Fatalf("mime changed: %s", mime)
	}
}

func TestPrepareImage_UnrecognizedFormat(t *testing.T) {
	// Garbage bytes should pass through unchanged.
	garbage := []byte("not an image")
	data, mime := PrepareImage(garbage, "image/jpeg")
	if !bytes.Equal(data, garbage) {
		t.Fatal("unrecognized format should pass through")
	}
	if mime != "image/jpeg" {
		t.Fatalf("mime changed: %s", mime)
	}
}

func TestResizeNearest_Downscale(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2048, 2048))
	for y := 0; y < 2048; y++ {
		for x := 0; x < 2048; x++ {
			img.Set(x, y, color.RGBA{R: 0, G: 255, B: 0, A: 255})
		}
	}
	resized := resizeNearest(img, MaxImageDim)
	b := resized.Bounds()
	if b.Dx() > MaxImageDim {
		t.Fatalf("width %d exceeds max %d", b.Dx(), MaxImageDim)
	}
	if b.Dy() > MaxImageDim {
		t.Fatalf("height %d exceeds max %d", b.Dy(), MaxImageDim)
	}
}
