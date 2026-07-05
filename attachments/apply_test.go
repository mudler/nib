package attachments

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestApply(t *testing.T) {
	extract := func(path string) (string, error) { return "DOC TEXT", nil }
	transcribe := func(_ context.Context, path string) (string, error) { return "SPOKEN", nil }

	// data URI needs real files for image; fake the image via a temp file
	dir := t.TempDir()
	img := dir + "/pic.png"
	if err := os.WriteFile(img, []byte("\x89PNGdata"), 0o644); err != nil {
		t.Fatalf("write temp image: %v", err)
	}

	files := []string{img, "notes.pdf", "memo.m4a"}
	caps := ModelCapabilities{InputModalities: []string{"text", "image"}} // vision, no audio
	res, err := Apply(context.Background(), files, caps, nil, extract, transcribe)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Parts) != 1 || res.Parts[0].Kind != KindImage {
		t.Fatalf("expected 1 image part, got %+v", res.Parts)
	}
	if !strings.Contains(res.TextPreamble, "DOC TEXT") || !strings.Contains(res.TextPreamble, "SPOKEN") {
		t.Fatalf("preamble missing doc/transcript: %q", res.TextPreamble)
	}
	if !strings.Contains(res.TextPreamble, "[Attached: notes.pdf") {
		t.Fatalf("preamble missing label: %q", res.TextPreamble)
	}
}
