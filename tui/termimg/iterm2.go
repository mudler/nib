package termimg

import (
	"encoding/base64"
	"fmt"
)

// EncodeITerm2 encodes image data as an iTerm2 inline image escape
// sequence (OSC 1337). The image is displayed inline at the cursor
// position.
//
// Unlike kitty's transmit-once model, iTerm2 re-emits the full base64
// payload on every frame. This is acceptable — iTerm2 handles it
// efficiently and there is no transmit state to track.
//
// cols and rows specify the display size in terminal cells. If both are
// 0, the terminal uses the image's natural size.
func EncodeITerm2(data []byte, mime string, cols, rows int) string {
	b64 := base64.StdEncoding.EncodeToString(data)
	var w, h string
	if cols > 0 {
		w = fmt.Sprintf("width=%d;", cols)
	}
	if rows > 0 {
		h = fmt.Sprintf("height=%d;", rows)
	}
	return fmt.Sprintf("\x1b]1337;File=inline=1;%s%s:%s\x07", w, h, b64)
}
