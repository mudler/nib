package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mudler/nib/mcp"
	openai "github.com/sashabaranov/go-openai"
)

// The test session has a 1000-token window, so the budget is 500 byte/4
// tokens (2000 bytes).

func trimFragment(s *Session) []openai.ChatCompletionMessage {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	return append([]openai.ChatCompletionMessage(nil), s.fragment.Messages...)
}

func assertPaired(t *testing.T, msgs []openai.ChatCompletionMessage) {
	t.Helper()
	if err := validateToolPairing(msgs); err != nil {
		t.Fatalf("tool pairing broken: %v", err)
	}
}

func TestIterativeTrimStep1CompactionFits(t *testing.T) {
	big := strings.Repeat("x", 400)
	frag := []openai.ChatCompletionMessage{
		{Role: "user", Content: "u1" + big},
		{Role: "assistant", Content: "a1" + big},
		{Role: "user", Content: "u2" + big},
		{Role: "assistant", Content: "a2" + big},
		{Role: "user", Content: "u3" + big},
		{Role: "assistant", Content: "a3" + big},
	}
	llm := &fakeSummaryLLM{reply: "SUMMARY-TEXT"}
	s := newCompactTestSession(llm, 2, frag, frag)

	if err := s.iterativeTrim(context.Background()); err != nil {
		t.Fatalf("iterativeTrim: %v", err)
	}
	// The head (u1..a2) is larger than the summary prompt budget, so step 1
	// summarizes it in exactly 2 rolling chunks. The [summary, u3, a3]
	// shape below shows it was step 1 (KeepRecent 2), not the shrunk keep.
	if llm.calls != 2 {
		t.Fatalf("want 2 summary chunk calls from step 1, got %d", llm.calls)
	}
	got := trimFragment(s)
	if len(got) != 3 || !strings.Contains(got[0].Content, "SUMMARY-TEXT") {
		t.Fatalf("want [summary, u3, a3], got %d messages: %q", len(got), got[0].Content)
	}
	if estimateTokens(got) > ContextBudget(s.compactionConfig(), s.contextWindow()) {
		t.Fatalf("result over budget: %d", estimateTokens(got))
	}
	assertPaired(t, got)
}

func TestIterativeTrimNoOpFallsThroughToShrinkKeep(t *testing.T) {
	big := strings.Repeat("x", 600)
	frag := []openai.ChatCompletionMessage{
		{Role: "user", Content: "u1" + big},
		{Role: "assistant", Content: "a1" + big},
		{Role: "user", Content: "u2" + big},
		{Role: "assistant", Content: "a2" + big},
	}
	llm := &fakeSummaryLLM{reply: "SUMMARY-TEXT"}
	// KeepRecent 10 > len(frag): compactHistory has nothing to compact.
	s := newCompactTestSession(llm, 10, frag, frag)

	// Step 1 on its own: nothing to compact, so no summary call and no change.
	if _, _, err := s.compactHistory(context.Background()); err != nil {
		t.Fatalf("compactHistory: %v", err)
	}
	if llm.calls != 0 {
		t.Fatalf("want 0 summary calls from step 1, got %d", llm.calls)
	}
	if got := trimFragment(s); len(got) != len(frag) {
		t.Fatalf("step 1 changed the fragment: %d messages", len(got))
	}

	if err := s.iterativeTrim(context.Background()); err != nil {
		t.Fatalf("iterativeTrim: %v", err)
	}
	// Step 1 makes no call, so every call comes from the shrunk keep: its
	// head is larger than the summary prompt budget and goes out in exactly
	// 2 rolling chunks.
	if llm.calls != 2 {
		t.Fatalf("want exactly 2 summary chunk calls (from the shrunk keep), got %d", llm.calls)
	}
	got := trimFragment(s)
	if len(got) >= len(frag) || !strings.Contains(got[0].Content, "SUMMARY-TEXT") {
		t.Fatalf("want a compacted fragment from step 2, got %d messages", len(got))
	}
	if s.compaction.KeepRecent != 10 {
		t.Fatalf("shared config mutated: KeepRecent=%d", s.compaction.KeepRecent)
	}
	assertPaired(t, got)
}

func toolExchange(id, output string) []openai.ChatCompletionMessage {
	return []openai.ChatCompletionMessage{
		{Role: "assistant", ToolCalls: []openai.ToolCall{{ID: id, Type: "function",
			Function: openai.FunctionCall{Name: "bash", Arguments: `{"command":"ls"}`}}}},
		{Role: "tool", ToolCallID: id, Content: output},
	}
}

func TestIterativeTrimFallsThroughToAggressivePrune(t *testing.T) {
	out := strings.Repeat("o", 2400)
	frag := []openai.ChatCompletionMessage{{Role: "user", Content: "u1"}}
	frag = append(frag, toolExchange("c1", out)...)
	frag = append(frag, toolExchange("c2", out)...)
	frag = append(frag, openai.ChatCompletionMessage{Role: "assistant", Content: "done"})
	frag = append(frag, openai.ChatCompletionMessage{Role: "user", Content: "next"})

	llm := &fakeSummaryLLM{err: errors.New("backend down")}
	s := newCompactTestSession(llm, 2, frag, nil)

	if err := s.iterativeTrim(context.Background()); err != nil {
		t.Fatalf("iterativeTrim: %v", err)
	}
	if llm.calls != 1 {
		t.Fatalf("a failed summary must not be retried with a smaller keep; got %d calls", llm.calls)
	}
	got := trimFragment(s)
	if len(got) != len(frag) {
		t.Fatalf("prune must keep every message, got %d want %d", len(got), len(frag))
	}
	for _, m := range got {
		if m.Role == "tool" && m.Content == out {
			t.Fatalf("tool output %s was not stubbed", m.ToolCallID)
		}
	}
	if estimateTokens(got) > ContextBudget(s.compactionConfig(), s.contextWindow()) {
		t.Fatalf("result over budget: %d", estimateTokens(got))
	}
	assertPaired(t, got)
	// The stubs are recorded, so the next request's prune sends the same bytes.
	s.prunedMu.Lock()
	_, ok1 := s.prunedIDs["c1"]
	_, ok2 := s.prunedIDs["c2"]
	s.prunedMu.Unlock()
	if !ok1 || !ok2 {
		t.Fatalf("stubbed ids not recorded in prunedIDs")
	}
	next := s.pruneMessages(got)
	for i := range got {
		if next[i].Content != got[i].Content {
			t.Fatalf("message %d changes on the next request: %q -> %q", i, got[i].Content, next[i].Content)
		}
	}
}

func TestIterativeTrimHardTruncates(t *testing.T) {
	big := strings.Repeat("x", 600)
	frag := []openai.ChatCompletionMessage{
		{Role: "user", Content: "u1" + big},
		{Role: "assistant", Content: "a1" + big},
		{Role: "user", Content: "u2" + big},
		{Role: "assistant", Content: "a2" + big},
		{Role: "user", Content: "u3" + big},
		{Role: "assistant", Content: "a3" + big},
	}
	llm := &fakeSummaryLLM{err: errors.New("backend down")}
	s := newCompactTestSession(llm, 2, frag, frag)
	s.artifacts = mcp.NewArtifactStore()

	if err := s.iterativeTrim(context.Background()); err != nil {
		t.Fatalf("iterativeTrim: %v", err)
	}
	got := trimFragment(s)
	if got[0].Role != "user" || !strings.Contains(got[0].Content, "truncated to fit context window") ||
		!strings.Contains(got[0].Content, "artifact://1") {
		t.Fatalf("want a user-role truncation marker naming artifact://1, got %s %q", got[0].Role, got[0].Content)
	}
	if last := got[len(got)-1]; last.Content != frag[len(frag)-1].Content {
		t.Fatalf("the most recent message must be kept verbatim")
	}
	if estimateTokens(got) > ContextBudget(s.compactionConfig(), s.contextWindow()) {
		t.Fatalf("result over budget: %d", estimateTokens(got))
	}
	art := s.artifacts.Get(1)
	if art == nil || !strings.Contains(art.Content, "u1") || !strings.Contains(art.Content, "a1") {
		t.Fatalf("artifact must hold the dropped history")
	}
	assertPaired(t, got)
	if len(s.messages) == 0 || s.messages[0].Role != "assistant" {
		t.Fatalf("display copy not rebuilt: %+v", s.messages)
	}
}

func TestIterativeTrimAllStepsFail(t *testing.T) {
	frag := []openai.ChatCompletionMessage{
		{Role: "user", Content: "u1" + strings.Repeat("x", 400)},
		{Role: "assistant", Content: "a1" + strings.Repeat("x", 400)},
		{Role: "user", Content: strings.Repeat("y", 4000)}, // alone over budget
	}
	llm := &fakeSummaryLLM{err: errors.New("backend down")}
	s := newCompactTestSession(llm, 2, frag, frag)

	err := s.iterativeTrim(context.Background())
	if err == nil {
		t.Fatal("want an error when no step fits the budget")
	}
	if !errors.Is(err, ErrCompactionOverBudget) {
		t.Fatalf("want ErrCompactionOverBudget, got %v", err)
	}
	got := trimFragment(s)
	if len(got) != len(frag) {
		t.Fatalf("fragment changed on failure: %d messages", len(got))
	}
	for i := range frag {
		if got[i].Content != frag[i].Content {
			t.Fatalf("message %d changed on failure", i)
		}
	}
}
