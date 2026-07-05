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
		},
	}
	text, parts := composeAttachments("summarize", res)
	if !strings.HasPrefix(text, "[Attached: a.pdf") || !strings.HasSuffix(text, "summarize") {
		t.Fatalf("preamble must precede user text: %q", text)
	}
	if len(parts) != 2 || parts[0].Kind != PartImage || parts[1].Kind != PartVideo {
		t.Fatalf("expected image+video ContentParts, got %+v", parts)
	}
}
