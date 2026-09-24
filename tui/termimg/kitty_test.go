package termimg

import (
	"strings"
	"testing"
)

func TestEncodeKittyTransmit(t *testing.T) {
	data := []byte("fake-png-data")
	seq := EncodeKittyTransmit(data, 42)

	if !strings.HasPrefix(seq, "\x1b_G") {
		t.Errorf("transmit sequence must start with APC _G, got %q", seq[:10])
	}
	if !strings.HasSuffix(seq, "\x1b\\") {
		t.Errorf("transmit sequence must end with ESC \\, got %q", seq[len(seq)-5:])
	}
	if !strings.Contains(seq, "a=t,") {
		t.Errorf("transmit must contain a=t (transmit action)")
	}
	if !strings.Contains(seq, "f=100") {
		t.Errorf("transmit must contain f=100 (PNG format)")
	}
	if !strings.Contains(seq, "i=42") {
		t.Errorf("transmit must contain i=42 (image ID)")
	}
	if !strings.Contains(seq, "q=2") {
		t.Errorf("transmit must contain q=2 (suppress response)")
	}
	if !strings.Contains(seq, "m=0") {
		t.Errorf("small payload must end with m=0 (last chunk)")
	}
}

func TestEncodeKittyTransmitChunked(t *testing.T) {
	// Create data large enough to require multiple chunks.
	// chunkSize is 4096 bytes of base64. 3*4096 bytes of base64
	// requires ~3*3072 bytes of raw data.
	data := make([]byte, 10000)
	for i := range data {
		data[i] = byte(i % 256)
	}
	seq := EncodeKittyTransmit(data, 1)

	// Should contain at least one m=1 (more chunks follow).
	if !strings.Contains(seq, "m=1") {
		t.Errorf("large payload must contain m=1 (more chunks)")
	}
	// Must end with m=0 (last chunk).
	if !strings.Contains(seq, "m=0") {
		t.Errorf("payload must end with m=0 (last chunk)")
	}
}

func TestEncodeKittyPlace(t *testing.T) {
	seq := EncodeKittyPlace(7, 80, 24)

	if !strings.HasPrefix(seq, "\x1b_G") {
		t.Errorf("place sequence must start with APC _G")
	}
	if !strings.HasSuffix(seq, "\x1b\\") {
		t.Errorf("place sequence must end with ESC \\")
	}
	if !strings.Contains(seq, "a=p") {
		t.Errorf("place must contain a=p (place action)")
	}
	if !strings.Contains(seq, "i=7") {
		t.Errorf("place must contain i=7 (image ID)")
	}
	if !strings.Contains(seq, "c=80") {
		t.Errorf("place must contain c=80 (columns)")
	}
	if !strings.Contains(seq, "r=24") {
		t.Errorf("place must contain r=24 (rows)")
	}
}

func TestEncodeKittyDelete(t *testing.T) {
	seq := EncodeKittyDelete(5)

	if !strings.HasPrefix(seq, "\x1b_G") {
		t.Errorf("delete sequence must start with APC _G")
	}
	if !strings.HasSuffix(seq, "\x1b\\") {
		t.Errorf("delete sequence must end with ESC \\")
	}
	if !strings.Contains(seq, "a=d") {
		t.Errorf("delete must contain a=d (delete action)")
	}
	if !strings.Contains(seq, "d=I") {
		t.Errorf("delete must contain d=I (delete image + placements)")
	}
	if !strings.Contains(seq, "i=5") {
		t.Errorf("delete must contain i=5 (image ID)")
	}
}

func TestEncodeKittyDeleteAll(t *testing.T) {
	seq := EncodeKittyDeleteAll([]int{1, 2, 3})

	count := strings.Count(seq, "a=d")
	if count != 3 {
		t.Errorf("deleteAll must contain 3 delete actions, got %d", count)
	}
}
