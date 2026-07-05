package chat

import (
	"encoding/base64"
	"strings"

	"github.com/mudler/cogito"
)

type PartKind int

const (
	PartImage PartKind = iota
	PartAudio
	PartVideo
)

// ContentPart is nib's native multimodal part, satisfying cogito.TypedMultimedia
// so SendMessage can hand image/audio/video parts to the fragment. DataURI is a
// base64 data: URI (e.g. data:image/png;base64,...). AudioFormat is the audio
// container (e.g. "wav") used for the input_audio wire form.
type ContentPart struct {
	Kind        PartKind
	DataURI     string
	AudioFormat string
}

// URL returns the data URI (used for image_url / video_url parts).
func (p ContentPart) URL() string { return p.DataURI }

// Format returns the audio container for input_audio; "" for image/video.
func (p ContentPart) Format() string { return p.AudioFormat }

// MediaKind maps to cogito's media kind.
func (p ContentPart) MediaKind() cogito.MediaKind {
	switch p.Kind {
	case PartAudio:
		return cogito.MediaAudio
	case PartVideo:
		return cogito.MediaVideo
	default:
		return cogito.MediaImage
	}
}

// Data returns the raw base64 payload (no data: prefix) for input_audio; ""
// for image/video (which travel via URL()).
func (p ContentPart) Data() string {
	if p.Kind != PartAudio {
		return ""
	}
	if i := strings.Index(p.DataURI, ";base64,"); i >= 0 {
		return p.DataURI[i+len(";base64,"):]
	}
	// Fallback: if DataURI is already raw base64, validate & return as-is.
	if _, err := base64.StdEncoding.DecodeString(p.DataURI); err == nil {
		return p.DataURI
	}
	return ""
}
