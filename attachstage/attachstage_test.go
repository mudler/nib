package attachstage

import (
	"testing"

	"github.com/mudler/nib/attachments"
	"github.com/mudler/nib/slash"
)

func TestBuildSend(t *testing.T) {
	pending := []StagedFile{{Path: "a.wav", Transcribe: true}, {Path: "b.png"}}
	action := slash.Action{Kind: slash.KindSend, Text: "go", Files: []string{"c.pdf"}}
	files, overrides := BuildSend(pending, action)
	// staged first, then inline @path
	if len(files) != 3 || files[0] != "a.wav" || files[1] != "b.png" || files[2] != "c.pdf" {
		t.Fatalf("combine order wrong: %v", files)
	}
	if overrides["a.wav"] != attachments.OverrideTranscribe {
		t.Fatalf("a.wav must carry transcribe override: %v", overrides)
	}
	if _, ok := overrides["b.png"]; ok {
		t.Fatalf("non-transcribe staged file must not be in overrides")
	}
}
