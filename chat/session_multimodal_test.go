package chat

import (
	"testing"

	"github.com/mudler/cogito"
)

func TestBuildUserFragmentMultimodal(t *testing.T) {
	f := cogito.Fragment{}
	f = buildUserFragment(f, "look", []ContentPart{
		{Kind: PartImage, DataURI: "data:image/png;base64,AA"},
		{Kind: PartAudio, DataURI: "data:audio/wav;base64,BB", AudioFormat: "wav"},
	})
	last := f.Messages[len(f.Messages)-1]
	// image → image_url in MultiContent; audio → PendingNativeParts (cogito send-once)
	imageParts := 0
	for _, p := range last.MultiContent {
		if p.Type == "image_url" {
			imageParts++
		}
	}
	if imageParts != 1 {
		t.Fatalf("expected 1 image_url part, got %d", imageParts)
	}
	if len(f.PendingNativeParts) != 1 || f.PendingNativeParts[0].Kind != cogito.MediaAudio {
		t.Fatalf("expected 1 pending audio native part, got %+v", f.PendingNativeParts)
	}
}

func TestBuildUserFragmentTextOnly(t *testing.T) {
	f := buildUserFragment(cogito.Fragment{}, "hi", nil)
	last := f.Messages[len(f.Messages)-1]
	if last.Content != "hi" || len(last.MultiContent) != 0 {
		t.Fatalf("text-only must use scalar Content, got %+v", last)
	}
}
