package attachments

import "testing"

func caps(mods ...string) ModelCapabilities { return ModelCapabilities{InputModalities: mods} }

func TestRoute(t *testing.T) {
	cases := []struct {
		name     string
		kind     Kind
		caps     ModelCapabilities
		override Override
		want     Treatment
	}{
		{"image to vision", KindImage, caps("text", "image"), OverrideNone, SendAsImage},
		{"image to text-only", KindImage, caps("text"), OverrideNone, Block},
		{"audio to omni", KindAudio, caps("text", "audio"), OverrideNone, SendAsAudio},
		{"audio override transcribe", KindAudio, caps("text", "audio"), OverrideTranscribe, Transcribe},
		{"audio to text-only", KindAudio, caps("text"), OverrideNone, Transcribe},
		{"video to text-only", KindVideo, caps("text"), OverrideNone, Block},
		{"video to video model", KindVideo, caps("text", "video"), OverrideNone, SendAsVideo},
		{"document any", KindDocument, caps("text"), OverrideNone, ConvertToText},
		{"text any", KindText, caps("text"), OverrideNone, ConvertToText},
	}
	for _, tc := range cases {
		if got := Route(tc.kind, tc.caps, tc.override); got != tc.want {
			t.Errorf("%s: Route=%d want %d", tc.name, got, tc.want)
		}
	}
}

func TestSniff(t *testing.T) {
	for ext, want := range map[string]Kind{
		"a.png": KindImage, "b.jpg": KindImage, "c.m4a": KindAudio, "d.wav": KindAudio,
		"e.mp4": KindVideo, "f.pdf": KindDocument, "g.docx": KindDocument, "h.md": KindText,
	} {
		if got := Sniff(ext); got != want {
			t.Errorf("Sniff(%s)=%d want %d", ext, got, want)
		}
	}
}
