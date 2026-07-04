package attachments

import "slices"

type ModelCapabilities struct {
	InputModalities []string // "text","image","audio","video"
}

func (m ModelCapabilities) Accepts(mod string) bool { return slices.Contains(m.InputModalities, mod) }

type Override int

const (
	OverrideNone Override = iota
	OverrideTranscribe
)

type Treatment int

const (
	SendAsImage Treatment = iota
	SendAsAudio
	SendAsVideo
	Transcribe
	ConvertToText
	Block
)

// Route is pure: it maps a file kind + the active model's capabilities + an
// optional (audio-only) override to a treatment. See the spec's decision table.
func Route(kind Kind, caps ModelCapabilities, override Override) Treatment {
	switch kind {
	case KindImage:
		if caps.Accepts("image") {
			return SendAsImage
		}
		return Block
	case KindAudio:
		if override == OverrideTranscribe {
			return Transcribe
		}
		if caps.Accepts("audio") {
			return SendAsAudio
		}
		return Transcribe // text-only model → parakeet
	case KindVideo:
		if caps.Accepts("video") {
			return SendAsVideo
		}
		return Block // Phase 1: no extract-audio fallback yet
	case KindDocument, KindText:
		return ConvertToText
	default:
		return ConvertToText // best-effort: try to read unknown as text
	}
}
