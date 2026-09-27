package chat_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
	openai "github.com/sashabaranov/go-openai"
)

// TestResumeSeedsModelFromStoredContext is the regression test for a resumed
// session that had compacted: the record's display copy held only
// "Compacted N earlier messages" and the tail, and the model was seeded from
// it, so the summary and every tool result were gone. A record that kept the
// model context must seed the model from that, show the full display copy,
// and bring back the artifacts the context points at.
func TestResumeSeedsModelFromStoredContext(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	var mu sync.Mutex
	var last []capturedMessage
	srv := httptest.NewServer(messageCapturingOpenAI(func(m []capturedMessage) {
		mu.Lock()
		last = m
		mu.Unlock()
	}))
	defer srv.Close()

	display := []openai.ChatCompletionMessage{
		{Role: "user", Content: "fix the parser"},
		{Role: "assistant", Content: "the parser is fixed"},
		{Role: "assistant", Name: "nib_compaction_notice", Content: "Compacted 2 earlier messages"},
		{Role: "user", Content: "now the lexer"},
		{Role: "assistant", Content: "lexer done"},
	}
	context_ := []openai.ChatCompletionMessage{
		{Role: "user", Content: "SUMMARY: parser fixed in parse.go. Full text at artifact://3"},
		{Role: "user", Content: "now the lexer"},
		{Role: "assistant", ToolCalls: []openai.ToolCall{{ID: "c1", Type: "function", Function: openai.FunctionCall{Name: "read", Arguments: `{"path":"lex.go"}`}}}},
		{Role: "tool", ToolCallID: "c1", Content: "LEXER-SOURCE"},
		{Role: "assistant", Content: "lexer done"},
	}
	artifacts := []types.Artifact{{ID: 3, Tool: "compaction", Created: time.Unix(1, 0).UTC(), Content: "FULL-HEAD"}}

	cfg := types.Config{
		Model:            "fake-model",
		APIKey:           "fake-key",
		BaseURL:          srv.URL + "/v1",
		LogLevel:         "error",
		ApprovalMode:     "auto",
		AgentOptions:     types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 3},
		InitialHistory:   display,
		InitialContext:   context_,
		InitialArtifacts: artifacts,
	}
	session, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	if got := session.ExportHistory(); len(got) != len(display) || got[0].Content != "fix the parser" {
		t.Fatalf("display copy not restored whole: %+v", got)
	}
	if got := session.ExportArtifacts(); len(got) != 1 || got[0].ID != 3 || got[0].Content != "FULL-HEAD" {
		t.Fatalf("artifacts not restored: %+v", got)
	}

	if _, err := session.SendMessage("and the tests?"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	mu.Lock()
	got := last
	mu.Unlock()

	var sawSummary, sawTool, sawNotice bool
	for _, m := range got {
		sawSummary = sawSummary || strings.HasPrefix(m.Content, "SUMMARY:")
		sawTool = sawTool || (m.Role == "tool" && m.Content == "LEXER-SOURCE")
		sawNotice = sawNotice || strings.HasPrefix(m.Content, "Compacted ")
	}
	if !sawSummary || !sawTool {
		t.Fatalf("the model context must come from the stored context (summary %v, tool result %v): %+v", sawSummary, sawTool, got)
	}
	if sawNotice {
		t.Fatalf("the display copy's notice must not reach the model: %+v", got)
	}

	// The context a resumed session exports again has no system prompt, which
	// the next turn adds itself.
	for _, m := range session.ExportContext() {
		if m.Role == openai.ChatMessageRoleSystem {
			t.Fatalf("ExportContext must leave the system prompt out: %+v", m)
		}
	}
}

// TestSessionStoreKeepsContextAndArtifacts proves the new record fields
// survive a save and load.
func TestSessionStoreKeepsContextAndArtifacts(t *testing.T) {
	store := chat.NewSessionStore(t.TempDir())
	rec := chat.SessionRecord{
		ID:        "s1",
		Messages:  []openai.ChatCompletionMessage{{Role: "user", Content: "hi"}},
		Context:   []openai.ChatCompletionMessage{{Role: "user", Content: "SUMMARY"}},
		Artifacts: []types.Artifact{{ID: 1, Tool: "bash", Content: "out"}},
	}
	if err := store.Save(rec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.Load("s1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Context) != 1 || got.Context[0].Content != "SUMMARY" {
		t.Fatalf("Context = %+v", got.Context)
	}
	if len(got.Artifacts) != 1 || got.Artifacts[0].Content != "out" || got.Artifacts[0].ID != 1 {
		t.Fatalf("Artifacts = %+v", got.Artifacts)
	}
}
