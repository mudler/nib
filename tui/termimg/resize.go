package termimg

import (
	"bytes"
	"image"
	"image/png"
)

// MaxImageDim is the maximum width or height in pixels for an image
// transmitted to the terminal. Larger images are downscaled with
// nearest-neighbor sampling to keep base64 payload sizes manageable.
const MaxImageDim = 1024

// PrepareImage decodes raw image bytes, downscales if either dimension
// exceeds MaxImageDim, and re-encodes as PNG. Kitty requires PNG; iTerm2
// accepts other formats but PNG is a safe default.
//
// If the data is already a PNG within the size limit, it is returned
// unchanged. If decoding fails (unrecognized format), the original bytes
// are returned with the original MIME so the terminal can try to handle it.
func PrepareImage(data []byte, mime string) ([]byte, string) {
	if len(data) == 0 {
		return data, mime
	}

	// Fast path: PNG within size limits. We still need to decode to check
	// dimensions, but if decode fails we pass through.
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		// Unrecognized format — pass through unchanged.
		return data, mime
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= MaxImageDim && h <= MaxImageDim && mime == "image/png" {
		return data, mime
	}

	// Downscale if needed using nearest-neighbor.
	if w > MaxImageDim || h > MaxImageDim {
		img = resizeNearest(img, MaxImageDim)
	}

	// Re-encode as PNG.
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return data, mime
	}
	return buf.Bytes(), "image/png"
}

// resizeNearest scales an image so that neither dimension exceeds maxDim,
// preserving aspect ratio. Uses nearest-neighbor sampling — fast and
// sufficient for screenshots and tool-generated images where pixel
// accuracy matters more than smooth interpolation.
func resizeNearest(src image.Image, maxDim int) image.Image {
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	scale := 1.0
	if w > maxDim {
		scale = float64(maxDim) / float64(w)
	}
	if h > maxDim {
		s := float64(maxDim) / float64(h)
		if s < scale {
			scale = s
		}
	}

	newW := int(float64(w) * scale)
	newH := int(float64(h) * scale)
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	for y := 0; y < newH; y++ {
		srcY := bounds.Min.Y + y*h/newH
		for x := 0; x < newW; x++ {
			srcX := bounds.Min.X + x*w/newW
			dst.Set(x, y, src.At(srcX, srcY))
		}
	}
	return dst
}

// readAll is a small helper to drain a reader without importing ioutil.
func readAll(r []byte) []byte { return r }

var _ = readAll // reserved for future use
