package termimg

import (
	"strings"
	"testing"
)

func TestEncodeITerm2(t *testing.T) {
	data := []byte("fake-image-data")
	seq := EncodeITerm2(data, "image/png", 80, 24)

	if !strings.HasPrefix(seq, "\x1b]1337;File=") {
		t.Errorf("iTerm2 sequence must start with OSC 1337;File=, got %q", seq[:15])
	}
	if !strings.HasSuffix(seq, "\x07") {
		t.Errorf("iTerm2 sequence must end with BEL (\\x07)")
	}
	if !strings.Contains(seq, "inline=1") {
		t.Errorf("iTerm2 must contain inline=1")
	}
	if !strings.Contains(seq, "width=80") {
		t.Errorf("iTerm2 must contain width=80")
	}
	if !strings.Contains(seq, "height=24") {
		t.Errorf("iTerm2 must contain height=24")
	}
}

func TestEncodeITerm2NoDimensions(t *testing.T) {
	data := []byte("fake")
	seq := EncodeITerm2(data, "image/png", 0, 0)

	if strings.Contains(seq, "width=") {
		t.Errorf("iTerm2 with 0 cols must not contain width=")
	}
	if strings.Contains(seq, "height=") {
		t.Errorf("iTerm2 with 0 rows must not contain height=")
	}
}
