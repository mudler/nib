package fragmentreply

import (
	"testing"

	"github.com/mudler/cogito"
	"github.com/sashabaranov/go-openai"
)

var reply = openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: "ok"}

func TestAppendAuthoritativeFragmentStaysConsistent(t *testing.T) {
	f := cogito.NewEmptyFragment().WithConversation(cogito.WrapMessages(
		[]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}}))
	out := Append(f, reply)
	history, err := out.Conversation()
	if err != nil {
		t.Fatalf("Conversation() = %v", err)
	}
	if len(history) != 2 || history[1].Portable.Content != "ok" || len(out.Messages) != 2 {
		t.Fatalf("history = %+v, messages = %+v", history, out.Messages)
	}
}

func TestAppendLegacyFragmentStaysLegacy(t *testing.T) {
	out := Append(cogito.NewFragment(openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: "hi"}), reply)
	if out.ConversationMessages != nil || len(out.Messages) != 2 || out.Messages[1].Content != "ok" {
		t.Fatalf("legacy fragment changed shape: %+v", out)
	}
}
