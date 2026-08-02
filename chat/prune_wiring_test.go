package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
	openai "github.com/sashabaranov/go-openai"
)

// A reloaded config must take effect: an edit to tool_output_pruning that the
// session keeps ignoring is indistinguishable from a broken feature.
func TestReloadAppliesPruningConfig(t *testing.T) {
	s := newReloadTestSession(t)
	cfg := types.Config{ToolOutputPruning: types.ToolOutputPruningConfig{
		HighWaterTokens: 4242, LowWaterTokens: 100, MinResultTokens: 7,
	}}
	if err := s.Reload(cfg); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if s.pruning != cfg.ToolOutputPruning {
		t.Fatalf("pruning policy not reloaded: got %+v", s.pruning)
	}
}

// The wiring test. A policy nobody calls is the failure mode that passes every
// policy test — see the TUI refresh sites in PR #54, all four of which could be
// deleted with the suite still green.
func TestSessionPruneMessagesStubsAndRemembers(t *testing.T) {
	s := &Session{pruning: types.ToolOutputPruningConfig{
		HighWaterTokens: 1000, LowWaterTokens: 1, MinResultTokens: 1,
	}}

	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"a.go"}`), bigResult("c1", 9000),
		callMsg("c2", "read", `{"path":"b.go"}`), bigResult("c2", 9000),
		{Role: "user", Content: "next"},
	}

	out := s.pruneMessages(msgs)
	if out[1].Content == msgs[1].Content {
		t.Fatal("the oldest result was not stubbed")
	}
	if len(out) != len(msgs) {
		t.Fatalf("message count changed %d -> %d", len(msgs), len(out))
	}

	// Second call, same input: the session remembers, so nothing new is stubbed
	// and the previously stubbed result stays stubbed.
	stubbed := out[1].Content
	out2 := s.pruneMessages(msgs)
	if out2[1].Content != stubbed {
		t.Fatalf("stub changed between calls: %q -> %q", stubbed, out2[1].Content)
	}
}

// Disabled means the manipulator is a pass-through, not merely a cheaper path.
func TestSessionPruneMessagesDisabledIsIdentity(t *testing.T) {
	s := &Session{pruning: types.ToolOutputPruningConfig{Disabled: true, HighWaterTokens: 1, LowWaterTokens: 1, MinResultTokens: 1}}

	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"a.go"}`), bigResult("c1", 9000),
		{Role: "user", Content: "next"},
	}
	out := s.pruneMessages(msgs)
	for i := range msgs {
		if out[i].Content != msgs[i].Content {
			t.Fatalf("message %d changed while pruning was disabled", i)
		}
	}
}

// The stubbed-id set must not outlive the messages it describes. Compaction
// rewrites history and drops whole exchanges; ids kept for results that no
// longer exist would grow the set for the life of the session — an unbounded
// leak in a long-running agent, and one nothing else would ever clean up.
func TestSessionPruneMessagesForgetsIDsCompactionDropped(t *testing.T) {
	s := &Session{pruning: types.ToolOutputPruningConfig{
		HighWaterTokens: 1000, LowWaterTokens: 1, MinResultTokens: 1,
	}}

	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"a.go"}`), bigResult("c1", 9000),
		callMsg("c2", "read", `{"path":"b.go"}`), bigResult("c2", 9000),
		{Role: "user", Content: "next"},
	}
	s.pruneMessages(msgs)
	if !s.prunedIDs["c1"] || !s.prunedIDs["c2"] {
		t.Fatalf("expected both results stubbed, got %v", s.prunedIDs)
	}

	// Compaction replaced c1's whole exchange with a summary. c2 survives.
	compacted := []openai.ChatCompletionMessage{
		{Role: "system", Content: "summary of earlier work"},
		callMsg("c2", "read", `{"path":"b.go"}`), bigResult("c2", 9000),
		{Role: "user", Content: "next"},
	}
	out := s.pruneMessages(compacted)

	if s.prunedIDs["c1"] {
		t.Fatal("stubbed-id set retained an id whose result compaction removed")
	}
	if !s.prunedIDs["c2"] {
		t.Fatal("stubbed-id set forgot an id that is still in the conversation")
	}
	if out[2].Content == compacted[2].Content {
		t.Fatal("a surviving stubbed result un-stubbed itself")
	}
}

// A low-water mark above the high-water mark is a misconfiguration the sweep
// cannot honour: it triggers at the high mark and then immediately concludes it
// is already below the low mark, so crossing the mark prunes NOTHING. The wiring
// clamps low to high so the sweep at least frees enough to get back under the
// trigger.
func TestSessionPruneMessagesClampsLowWaterToHighWater(t *testing.T) {
	s := &Session{pruning: types.ToolOutputPruningConfig{
		HighWaterTokens: 1000, LowWaterTokens: 5000, MinResultTokens: 1,
	}}

	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"a.go"}`), bigResult("c1", 400),
		callMsg("c2", "read", `{"path":"b.go"}`), bigResult("c2", 400),
		callMsg("c3", "read", `{"path":"c.go"}`), bigResult("c3", 400),
		callMsg("c4", "read", `{"path":"d.go"}`), bigResult("c4", 400),
		{Role: "user", Content: "next"},
	}

	out := s.pruneMessages(msgs)
	if out[1].Content == msgs[1].Content {
		t.Fatal("high water was crossed but nothing was pruned")
	}
	// Clamped to high water, not to zero: the sweep stops as soon as the total
	// is back under the mark, so the newest results survive.
	if out[7].Content != msgs[7].Content {
		t.Fatal("the sweep pruned past the high-water mark")
	}
}

// The notice fires on the transition and only on the transition: a turn that
// merely re-applies existing stubs has freed nothing and must stay silent, or
// the user sees a "pruned" line on every LLM call for the rest of the session.
func TestSessionPruneMessagesNotifiesOnceOnTransition(t *testing.T) {
	var calls, gotResults, gotFreed int
	s := &Session{
		pruning: types.ToolOutputPruningConfig{
			HighWaterTokens: 1000, LowWaterTokens: 1, MinResultTokens: 1,
		},
		callbacks: Callbacks{OnPruneDone: func(results, freed int) {
			calls++
			gotResults, gotFreed = results, freed
		}},
	}

	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"a.go"}`), bigResult("c1", 9000),
		callMsg("c2", "read", `{"path":"b.go"}`), bigResult("c2", 9000),
		{Role: "user", Content: "next"},
	}
	s.pruneMessages(msgs)
	if calls != 1 {
		t.Fatalf("expected one notice, got %d", calls)
	}
	if gotResults != 2 {
		t.Fatalf("expected 2 stubbed results reported, got %d", gotResults)
	}
	if gotFreed < 17000 {
		t.Fatalf("expected roughly 18000 tokens freed, got %d", gotFreed)
	}

	s.pruneMessages(msgs)
	if calls != 1 {
		t.Fatalf("a second pass over the same messages fired another notice (%d total)", calls)
	}
}

// pruneRecorder is a fake OpenAI endpoint that records every request body and
// always answers with a plain stop message.
type pruneRecorder struct {
	mu     sync.Mutex
	bodies []string
}

func (p *pruneRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	p.mu.Lock()
	p.bodies = append(p.bodies, string(body))
	p.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "fake", "object": "chat.completion", "model": "fake",
		"choices": []any{map[string]any{
			"index": 0, "message": map[string]any{"role": "assistant", "content": "ok"},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	})
}

func (p *pruneRecorder) all() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.bodies, "\n")
}

// The end-to-end proof that the manipulator is installed on the RUN options: a
// real SendMessage, and the bytes the LLM received carry the stub instead of the
// oversized result. Deleting the WithMessagesManipulator line fails here.
func TestSessionSendMessagePrunesOnTheWire(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	rec := &pruneRecorder{}
	srv := httptest.NewServer(rec)
	defer srv.Close()

	cfg := types.Config{
		Model:        "fake-model",
		APIKey:       "fake-key",
		BaseURL:      srv.URL + "/v1",
		LogLevel:     "error",
		ApprovalMode: "auto",
		AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 3},
		ToolOutputPruning: types.ToolOutputPruningConfig{
			HighWaterTokens: 1000, LowWaterTokens: 1, MinResultTokens: 1,
		},
		InitialHistory: []openai.ChatCompletionMessage{
			{Role: "user", Content: "read a.go"},
			callMsg("c1", "read", `{"path":"a.go"}`),
			bigResult("c1", 9000),
			{Role: "assistant", Content: "read it"},
		},
	}

	session, err := NewSession(context.Background(), cfg, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	if _, err := session.SendMessage("now what?"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	sent := rec.all()
	if sent == "" {
		t.Fatal("the fake LLM received no request")
	}
	if !strings.Contains(sent, "dropped to save context") {
		t.Fatal("the request did not carry a pruned stub: the manipulator is not installed on the run")
	}
	if strings.Contains(sent, strings.Repeat("x", 5000)) {
		t.Fatal("the oversized tool result went out on the wire unpruned")
	}
	// The stored conversation is untouched: pruning rewrites the request only.
	hist := session.ExportHistory()
	var found bool
	for _, m := range hist {
		if m.ToolCallID == "c1" && len(m.Content) > 5000 {
			found = true
		}
	}
	if !found {
		t.Fatal("pruning mutated the session's own history")
	}
}
