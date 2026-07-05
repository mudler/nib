package attachments

import (
	"path/filepath"
	"strings"

	"github.com/mudler/nib/extraction"
)

type Kind int

const (
	KindImage Kind = iota
	KindAudio
	KindVideo
	KindDocument
	KindText
	KindUnknown
)

var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true}
var audioExts = map[string]bool{".mp3": true, ".wav": true, ".ogg": true, ".m4a": true, ".flac": true, ".mpeg": true, ".mpga": true}
var videoExts = map[string]bool{".mp4": true, ".avi": true, ".mkv": true, ".mov": true, ".webm": true, ".m4v": true}

// Sniff classifies a file by extension. Documents/text delegate to the
// extraction package's predicates so the two stay in sync.
func Sniff(path string) Kind {
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case imageExts[ext]:
		return KindImage
	case audioExts[ext]:
		return KindAudio
	case videoExts[ext]:
		return KindVideo
	case extraction.IsDocumentFile(path):
		return KindDocument
	case extraction.IsPlainTextFile(path):
		return KindText
	default:
		return KindUnknown
	}
}
