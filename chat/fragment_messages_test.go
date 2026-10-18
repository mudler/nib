package chat

import (
	"reflect"
	"testing"

	"github.com/mudler/cogito"
	"github.com/sashabaranov/go-openai"
)

func msg(role, content string) openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{Role: role, Content: content}
}

func richHistory() []cogito.Message {
	return []cogito.Message{
		{Portable: msg("system", "sys")},
		{Portable: msg("user", "a"), Provider: &cogito.ProviderMessage{Scope: cogito.ProviderScope{Provider: "p"}}},
		{Portable: msg("assistant", "b"), Provider: &cogito.ProviderMessage{Scope: cogito.ProviderScope{Provider: "p"}}},
		{Portable: msg("user", "c")},
	}
}

func TestReplaceMessagesLegacyFallback(t *testing.T) {
	f := cogito.NewFragment(msg("user", "old"))
	want := []openai.ChatCompletionMessage{msg("user", "new")}
	out := replaceMessages(f, want)
	if out.ConversationMessages != nil {
		t.Fatalf("legacy fragment gained authoritative history: %+v", out.ConversationMessages)
	}
	if !reflect.DeepEqual(out.Messages, want) {
		t.Fatalf("Messages = %+v, want %+v", out.Messages, want)
	}
}

func TestReplaceMessagesStaleFallback(t *testing.T) {
	f := cogito.NewEmptyFragment().WithConversation(richHistory())
	f.Messages = append(f.Messages, msg("assistant", "direct edit"))
	if _, err := f.Conversation(); err == nil {
		t.Fatal("setup: fragment should be stale")
	}
	want := []openai.ChatCompletionMessage{msg("user", "fresh")}
	out := replaceMessages(f, want)
	if out.ConversationMessages != nil {
		t.Fatalf("stale fragment kept authoritative history: %+v", out.ConversationMessages)
	}
	if !reflect.DeepEqual(out.Messages, want) {
		t.Fatalf("Messages = %+v, want %+v", out.Messages, want)
	}
}

func TestReplaceMessagesKeepsProviderDataOnPrefixAndSuffix(t *testing.T) {
	hist := richHistory()
	f := cogito.NewEmptyFragment().WithConversation(hist)
	// Replace the middle two messages with one new message.
	msgs := []openai.ChatCompletionMessage{hist[0].Portable, msg("user", "edited"), hist[3].Portable}
	out := replaceMessages(f, msgs)

	got, err := out.Conversation()
	if err != nil {
		t.Fatalf("Conversation() = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3: %+v", len(got), got)
	}
	if !reflect.DeepEqual(got[0], hist[0]) || !reflect.DeepEqual(got[2], hist[3]) {
		t.Fatalf("prefix or suffix changed: %+v", got)
	}
	if got[1].Provider != nil || got[1].Portable.Content != "edited" {
		t.Fatalf("middle = %+v, want new portable message without provider data", got[1])
	}
}

func TestReplaceMessagesRetainsEnvelopeOfUnchangedPrefix(t *testing.T) {
	hist := richHistory()
	f := cogito.NewEmptyFragment().WithConversation(hist)
	// Append a message: whole old history is the prefix.
	msgs := append(portableOf(hist), msg("assistant", "d"))
	out := replaceMessages(f, msgs)
	got, err := out.Conversation()
	if err != nil {
		t.Fatalf("Conversation() = %v", err)
	}
	if len(got) != 5 || got[1].Provider == nil || got[2].Provider == nil || got[4].Provider != nil {
		t.Fatalf("got = %+v", got)
	}
}

func TestReplaceMessagesDropsProviderDataOfRemovedMessages(t *testing.T) {
	hist := richHistory()
	f := cogito.NewEmptyFragment().WithConversation(hist)
	// Compaction: keep only the system message and the last user message.
	out := replaceMessages(f, []openai.ChatCompletionMessage{hist[0].Portable, hist[3].Portable})
	got, err := out.Conversation()
	if err != nil {
		t.Fatalf("Conversation() = %v", err)
	}
	if len(got) != 2 || got[0].Portable.Content != "sys" || got[1].Portable.Content != "c" {
		t.Fatalf("got = %+v", got)
	}
	for _, m := range got {
		if m.Provider != nil {
			t.Fatalf("provider data of a removed message leaked: %+v", got)
		}
	}
}

func portableOf(h []cogito.Message) []openai.ChatCompletionMessage { return cogito.PortableMessages(h) }
