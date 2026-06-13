package voice

import (
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

func TestApplyProfileAppendsVoiceInstructions(t *testing.T) {
	cfg := applyProfile(types.Config{Prompt: "BASE PROMPT"})
	if !strings.Contains(cfg.Prompt, "BASE PROMPT") {
		t.Fatal("original prompt must be retained")
	}
	if !strings.Contains(cfg.Prompt, "VOICE mode") {
		t.Fatal("voice instructions must be appended")
	}
}

func TestApplyProfileHandlesEmptyPrompt(t *testing.T) {
	cfg := applyProfile(types.Config{})
	if !strings.Contains(cfg.Prompt, "VOICE mode") {
		t.Fatal("voice instructions must be present even with empty base prompt")
	}
}
