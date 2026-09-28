package chat

import (
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

func systemMessages(s *Session) []string {
	var out []string
	for _, m := range s.fragment.Messages {
		if m.Role == "system" {
			out = append(out, m.Content)
		}
	}
	return out
}

// The system prompt names the model serving the session, so the model can
// answer "which model are you?" truthfully.
func TestSystemPromptNamesTheModel(t *testing.T) {
	s := &Session{llmModel: "gpt-5.5"}
	if err := s.Reload(types.Config{Prompt: "base prompt"}); err != nil {
		t.Fatal(err)
	}
	s.ensureSystemPrompt()
	sys := systemMessages(s)
	if len(sys) != 1 {
		t.Fatalf("got %d system messages, want 1", len(sys))
	}
	if !strings.HasPrefix(sys[0], "base prompt") || !strings.Contains(sys[0], "gpt-5.5") {
		t.Fatalf("system prompt does not name the model:\n%s", sys[0])
	}
}

// A model switch updates the stamp in place: the context keeps one system
// prompt, and it names the new model.
func TestModelSwitchRestampsTheSystemPrompt(t *testing.T) {
	s := &Session{llmModel: "old-model", baseURL: "http://unused.invalid/v1"}
	if err := s.Reload(types.Config{Prompt: "base prompt"}); err != nil {
		t.Fatal(err)
	}
	s.ensureSystemPrompt()
	s.fragment = s.fragment.AddMessage("user", "hi")

	s.SetModel("new-model")
	s.ensureSystemPrompt()

	sys := systemMessages(s)
	if len(sys) != 1 {
		t.Fatalf("got %d system messages after the switch, want 1: %q", len(sys), sys)
	}
	if !strings.Contains(sys[0], "new-model") || strings.Contains(sys[0], "old-model") {
		t.Fatalf("system prompt was not restamped:\n%s", sys[0])
	}
	if s.fragment.Messages[0].Role != "system" {
		t.Fatalf("the system prompt moved: first message is %q", s.fragment.Messages[0].Role)
	}
}
