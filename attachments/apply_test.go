package attachments

import (
	"context"
	"errors"
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

// TestApplyBlockedAccumulates verifies that a capability Block is non-fatal:
// a lone unsupported image on a text-only model is recorded in Result.Blocked,
// Apply returns a nil error, and neither extractor nor transcriber is invoked.
func TestApplyBlockedAccumulates(t *testing.T) {
	extract := func(path string) (string, error) {
		t.Fatalf("extract must not be called for a blocked image: %s", path)
		return "", nil
	}
	transcribe := func(_ context.Context, path string) (string, error) {
		t.Fatalf("transcribe must not be called for a blocked image: %s", path)
		return "", nil
	}

	// Real temp file so Sniff→Route sees KindImage (Block needs no DataURI read).
	dir := t.TempDir()
	img := dir + "/pic.png"
	if err := os.WriteFile(img, []byte("\x89PNGdata"), 0o644); err != nil {
		t.Fatalf("write temp image: %v", err)
	}

	caps := ModelCapabilities{InputModalities: []string{"text"}} // text-only → image blocked
	res, err := Apply(context.Background(), []string{img}, caps, nil, extract, transcribe)
	if err != nil {
		t.Fatalf("Apply: unexpected error: %v", err)
	}
	if len(res.Parts) != 0 {
		t.Fatalf("expected no parts, got %+v", res.Parts)
	}
	if len(res.Blocked) != 1 {
		t.Fatalf("expected 1 blocked file, got %+v", res.Blocked)
	}
	if res.Blocked[0].Path != img {
		t.Fatalf("expected blocked path %q, got %q", img, res.Blocked[0].Path)
	}
}

// TestApplyExtractErrorAborts verifies Apply's fail-fast contract: the first
// extractor error aborts the batch, is returned to the caller, and nothing
// partial leaks into the returned Result.
func TestApplyExtractErrorAborts(t *testing.T) {
	extract := func(_ string) (string, error) { return "", errors.New("boom") }
	transcribe := func(_ context.Context, _ string) (string, error) { return "SPOKEN", nil }

	// notes.pdf routes to ConvertToText; the fake extractor runs, so no real file needed.
	caps := ModelCapabilities{InputModalities: []string{"text"}}
	res, err := Apply(context.Background(), []string{"notes.pdf"}, caps, nil, extract, transcribe)
	if err == nil {
		t.Fatalf("expected error from failing extractor, got nil")
	}
	if len(res.Parts) != 0 {
		t.Fatalf("fail-fast: expected no parts, got %+v", res.Parts)
	}
	if res.TextPreamble != "" {
		t.Fatalf("fail-fast: expected empty preamble, got %q", res.TextPreamble)
	}
}
