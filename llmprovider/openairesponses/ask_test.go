package openairesponses

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mudler/cogito"
	openai "github.com/sashabaranov/go-openai"
)

func TestAskKeepsAuthoritativeFragmentConsistent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"r1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	l := New(Config{Model: "gpt-5", BaseURL: srv.URL, APIKey: "k"})
	in := cogito.NewEmptyFragment().WithConversation(cogito.WrapMessages([]openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hi"},
	}))
	out, err := l.Ask(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	history, err := out.Conversation()
	if err != nil {
		t.Fatalf("Conversation() after Ask: %v", err)
	}
	if len(history) != 2 || history[1].Portable.Content != "hello" {
		t.Fatalf("history = %+v, want user message then reply", history)
	}
}

func TestAskLegacyFragmentStaysLegacy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"r1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	l := New(Config{Model: "gpt-5", BaseURL: srv.URL, APIKey: "k"})
	in := cogito.NewEmptyFragment().AddMessage(openai.ChatMessageRoleUser, "hi")
	out, err := l.Ask(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.ConversationMessages != nil {
		t.Fatalf("legacy fragment gained authoritative history: %+v", out.ConversationMessages)
	}
	if len(out.Messages) != 2 || out.Messages[1].Content != "hello" {
		t.Fatalf("Messages = %+v, want user message then reply", out.Messages)
	}
}
