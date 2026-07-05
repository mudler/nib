package chat

import (
	"strings"
	"testing"

	"github.com/mudler/nib/attachments"
)

func TestComposeAttachments(t *testing.T) {
	res := attachments.Result{
		TextPreamble: "[Attached: a.pdf — converted to text]\nBODY\n[End of a.pdf]\n\n",
		Parts: []attachments.Part{
			{Kind: attachments.KindImage, DataURI: "data:image/png;base64,AA"},
			{Kind: attachments.KindVideo, DataURI: "data:video/mp4;base64,BB"},
			{Kind: attachments.KindAudio, DataURI: "data:audio/wav;base64,AA", Format: "wav"},
		},
	}
	text, parts := composeAttachments("summarize", res)
	if !strings.HasPrefix(text, "[Attached: a.pdf") || !strings.HasSuffix(text, "summarize") {
		t.Fatalf("preamble must precede user text: %q", text)
	}
	if len(parts) != 3 || parts[0].Kind != PartImage || parts[1].Kind != PartVideo || parts[2].Kind != PartAudio {
		t.Fatalf("expected image+video+audio ContentParts, got %+v", parts)
	}
	// audio Part.Format must propagate to ContentPart.AudioFormat (input_audio.format).
	if parts[2].AudioFormat != "wav" {
		t.Fatalf("expected audio AudioFormat %q, got %q", "wav", parts[2].AudioFormat)
	}
	// image/video carry no audio format.
	if parts[0].AudioFormat != "" || parts[1].AudioFormat != "" {
		t.Fatalf("image/video must have empty AudioFormat, got %q %q", parts[0].AudioFormat, parts[1].AudioFormat)
	}
}
