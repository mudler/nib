package chat

import (
	"testing"

	"github.com/mudler/cogito"
)

func TestContentPartImplementsTypedMultimedia(t *testing.T) {
	var _ cogito.TypedMultimedia = ContentPart{}

	a := ContentPart{Kind: PartAudio, DataURI: "data:audio/wav;base64,AA", AudioFormat: "wav"}
	if a.MediaKind() != cogito.MediaAudio {
		t.Fatalf("audio → cogito.MediaAudio, got %v", a.MediaKind())
	}
	if a.URL() != "data:audio/wav;base64,AA" || a.Format() != "wav" {
		t.Fatalf("URL/Format mismatch: %q %q", a.URL(), a.Format())
	}

	v := ContentPart{Kind: PartVideo, DataURI: "data:video/mp4;base64,BB"}
	if v.MediaKind() != cogito.MediaVideo {
		t.Fatalf("video → cogito.MediaVideo, got %v", v.MediaKind())
	}
	i := ContentPart{Kind: PartImage, DataURI: "data:image/png;base64,CC"}
	if i.MediaKind() != cogito.MediaImage {
		t.Fatalf("image → cogito.MediaImage, got %v", i.MediaKind())
	}
	// audio carries base64 via Data(); image/video via URL()
	if a.Data() != "" && i.Data() != "" {
		t.Log("Data() is audio-only; image uses URL()")
	}
}
