package termimg

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// Kitty graphics protocol escape sequences.
//
// Kitty uses APC (Application Program Command) sequences:
//   ESC _ G <payload> ESC \
//
// The protocol supports transmit-once + placement: an image is sent once
// (a=t) with a stable numeric ID, then displayed (a=p) by referencing
// the ID. This avoids re-sending base64 data on every frame redraw.

const (
	kittyBegin = "\x1b_G"
	kittyEnd   = "\x1b\\"
	// chunkSize is the max base64 payload per APC sequence. Kitty
	// recommends keeping APC payloads under 4096 bytes.
	chunkSize = 4096
)

// EncodeKittyTransmit encodes PNG image data as a kitty graphics transmit
// sequence. The image is sent once and stored by the terminal under
// imageID. Subsequent displays use EncodeKittyPlace with the same ID.
//
// The data must be PNG-encoded (kitty format f=100).
func EncodeKittyTransmit(data []byte, imageID int) string {
	var b strings.Builder
	b64 := base64.StdEncoding.EncodeToString(data)

	for i := 0; i < len(b64); i += chunkSize {
		end := i + chunkSize
		more := 1 // m=1: more chunks follow
		if end >= len(b64) {
			end = len(b64)
			more = 0 // m=0: last chunk
		}
		chunk := b64[i:end]
		fmt.Fprintf(&b, "%sa=t,f=100,q=2,i=%d,m=%d;%s%s", kittyBegin, imageID, more, chunk, kittyEnd)
	}
	return b.String()
}

// EncodeKittyPlace emits a kitty placement sequence that displays a
// previously transmitted image at the current cursor position. The image
// occupies cols × rows terminal cells.
func EncodeKittyPlace(imageID, cols, rows int) string {
	return fmt.Sprintf("%sa=p,i=%d,c=%d,r=%d,q=2%s", kittyBegin, imageID, cols, rows, kittyEnd)
}

// EncodeKittyDelete emits a kitty delete sequence that purges an image
// and all its placements from the terminal's store (including
// scrollback). Call this when an image is evicted from the budget or
// when the program exits.
func EncodeKittyDelete(imageID int) string {
	return fmt.Sprintf("%sa=d,d=I,i=%d,q=2%s", kittyBegin, imageID, kittyEnd)
}

// EncodeKittyDeleteAll purges all transmitted kitty images. Call on
// program exit to avoid leaking graphics state in the terminal.
func EncodeKittyDeleteAll(imageIDs []int) string {
	var b strings.Builder
	for _, id := range imageIDs {
		b.WriteString(EncodeKittyDelete(id))
	}
	return b.String()
}
