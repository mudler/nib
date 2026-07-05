package attachments

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/mudler/nib/specialist"
)

type Extractor func(path string) (string, error)
type Transcriber func(ctx context.Context, path string) (string, error)

type Part struct {
	Kind    Kind
	DataURI string
}
type Blocked struct {
	Path   string
	Reason string
}
type Result struct {
	Parts        []Part
	TextPreamble string
	Blocked      []Blocked
}

func label(path, treatment, text string) string {
	base := filepath.Base(path)
	return fmt.Sprintf("[Attached: %s — %s]\n%s\n[End of %s]\n\n", base, treatment, text, base)
}

// Apply resolves each file to its treatment and executes it, producing the native
// parts + text preamble SendMessage needs, plus any blocked files for the caller
// to surface. overrides maps a file path to an audio override (may be nil).
func Apply(ctx context.Context, files []string, caps ModelCapabilities, overrides map[string]Override,
	extract Extractor, transcribe Transcriber) (Result, error) {
	var res Result
	for _, path := range files {
		kind := Sniff(path)
		ov := OverrideNone
		if overrides != nil {
			ov = overrides[path]
		}
		switch Route(kind, caps, ov) {
		case SendAsImage:
			uri, err := specialist.DataURI(path)
			if err != nil {
				return res, err
			}
			res.Parts = append(res.Parts, Part{Kind: KindImage, DataURI: uri})
		case SendAsAudio:
			uri, err := specialist.DataURI(path)
			if err != nil {
				return res, err
			}
			res.Parts = append(res.Parts, Part{Kind: KindAudio, DataURI: uri})
		case SendAsVideo:
			uri, err := specialist.DataURI(path)
			if err != nil {
				return res, err
			}
			res.Parts = append(res.Parts, Part{Kind: KindVideo, DataURI: uri})
		case Transcribe:
			text, err := transcribe(ctx, path)
			if err != nil {
				return res, err
			}
			res.TextPreamble += label(path, "transcribed", text)
		case ConvertToText:
			text, err := extract(path)
			if err != nil {
				return res, err
			}
			res.TextPreamble += label(path, "converted to text", text)
		case Block:
			res.Blocked = append(res.Blocked, Blocked{Path: path, Reason: "active model can't accept this file type"})
		}
	}
	return res, nil
}
