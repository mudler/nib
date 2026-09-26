package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mudler/nib/mcp"
	openai "github.com/sashabaranov/go-openai"
)

// The test session has a 1000-token window, so the budget is 500 byte/4
// tokens (2000 bytes).

func basicToolCall(id, name, args string) openai.ToolCall {
	return openai.ToolCall{ID: id, Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: name, Arguments: args}}
}

func TestBasicCompactRendersHeadAndKeepsTail(t *testing.T) {
	bigResult := "RESULT-BODY" + strings.Repeat("r", 3000)
	longAssistant := "a1" + strings.Repeat("z", 400)
	frag := []openai.ChatCompletionMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1 please do X\n  exactly like this"},
		{Role: "assistant", ToolCalls: []openai.ToolCall{basicToolCall("c1", "read", `{"path":"/a/b.go"}`)}},
		{Role: "tool", ToolCallID: "c1", Content: bigResult},
		{Role: "assistant", Content: longAssistant},
		{Role: "user", Content: "u2 now run ls"},
		{Role: "assistant", ToolCalls: []openai.ToolCall{basicToolCall("c2", "bash", `{"command":"ls"}`)}},
		{Role: "tool", ToolCallID: "c2", Content: "ok"},
		{Role: "assistant", Content: "done"},
	}
	llm := &fakeSummaryLLM{reply: "SUMMARY-TEXT"}
	s := newCompactTestSession(llm, 3, frag, frag)
	s.artifacts = mcp.NewArtifactStore()

	if err := s.basicCompact(context.Background()); err != nil {
		t.Fatalf("basicCompact: %v", err)
	}
	if llm.calls != 0 {
		t.Fatalf("basicCompact must not call the LLM, got %d calls", llm.calls)
	}
	got := trimFragment(s)
	if len(got) != 5 {
		t.Fatalf("want [system, marker, 3 tail], got %d: %+v", len(got), got)
	}
	if got[0].Role != "system" || got[0].Content != "sys" {
		t.Fatalf("head system message must be kept first, got %s %q", got[0].Role, got[0].Content)
	}
	body := got[1].Content
	if got[1].Role != "user" || !strings.Contains(body, "basic fallback mode") || !strings.Contains(body, "artifact://1") {
		t.Fatalf("want a user-role marker naming artifact://1, got %s %q", got[1].Role, body)
	}
	for _, want := range []string{
		"u1 please do X\n  exactly like this",
		"u2 now run ls",
		"[tool read(/a/b.go) ran, result elided]",
		"Assistant: " + longAssistant[:200],
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("marker missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, longAssistant[:201]) || strings.Contains(body, "RESULT-BODY") {
		t.Fatalf("assistant text must be cut to 200 chars and tool output elided:\n%s", body)
	}
	for i, m := range got[2:] {
		orig := frag[6+i]
		if m.Role != orig.Role || m.Content != orig.Content || m.ToolCallID != orig.ToolCallID || len(m.ToolCalls) != len(orig.ToolCalls) {
			t.Fatalf("tail message %d not kept verbatim: %+v", i, m)
		}
	}
	for _, m := range got {
		if m.Role == "tool" && m.ToolCallID == "c1" {
			t.Fatalf("head tool result must not survive as a tool message")
		}
	}
	assertPaired(t, got)
	if estimateTokens(got) > s.trimBudget(s.compactionConfig()) {
		t.Fatalf("result over budget: %d", estimateTokens(got))
	}
	art := s.artifacts.Get(1)
	if art == nil || !strings.Contains(art.Content, "RESULT-BODY") || !strings.Contains(art.Content, "u1 please do X") {
		t.Fatalf("artifact must hold the full untruncated head, got %+v", art)
	}
	if len(s.messages) == 0 || s.messages[0].Role != "assistant" || s.messages[len(s.messages)-1].Content != "done" {
		t.Fatalf("display copy not rebuilt: %+v", s.messages)
	}
}

func TestBasicCompactDropsActivityLinesWhenTight(t *testing.T) {
	var frag []openai.ChatCompletionMessage
	for i := range 10 {
		frag = append(frag,
			openai.ChatCompletionMessage{Role: "user", Content: fmt.Sprintf("user-%d", i)},
			openai.ChatCompletionMessage{Role: "assistant", Content: fmt.Sprintf("asst-%d-", i) + strings.Repeat("q", 250)},
		)
	}
	frag = append(frag,
		openai.ChatCompletionMessage{Role: "user", Content: "last-user" + strings.Repeat("t", 400)},
		openai.ChatCompletionMessage{Role: "assistant", Content: "last-asst" + strings.Repeat("t", 400)},
	)
	s := newCompactTestSession(&fakeSummaryLLM{}, 2, frag, frag)
	s.artifacts = mcp.NewArtifactStore()

	if err := s.basicCompact(context.Background()); err != nil {
		t.Fatalf("basicCompact: %v", err)
	}
	got := trimFragment(s)
	if estimateTokens(got) > s.trimBudget(s.compactionConfig()) {
		t.Fatalf("result over budget: %d", estimateTokens(got))
	}
	body := got[0].Content
	for i := range 10 {
		if !strings.Contains(body, fmt.Sprintf("user-%d\n", i)) {
			t.Fatalf("user-%d must be preserved while activity lines can go:\n%s", i, body)
		}
	}
	if strings.Contains(body, "asst-0-") {
		t.Fatalf("the oldest activity line must be dropped first:\n%s", body)
	}
	if !strings.Contains(body, "asst-9-") {
		t.Fatalf("the newest activity line should survive:\n%s", body)
	}
	if got[len(got)-1].Content != frag[len(frag)-1].Content {
		t.Fatalf("tail must be kept verbatim")
	}
}

func TestBasicCompactDropsOldUsersAsLastResort(t *testing.T) {
	var frag []openai.ChatCompletionMessage
	for i := range 8 {
		frag = append(frag,
			openai.ChatCompletionMessage{Role: "user", Content: fmt.Sprintf("user-%d-", i) + strings.Repeat("w", 400)},
			openai.ChatCompletionMessage{Role: "assistant", Content: fmt.Sprintf("asst-%d", i)},
		)
	}
	frag = append(frag,
		openai.ChatCompletionMessage{Role: "user", Content: "last-user"},
		openai.ChatCompletionMessage{Role: "assistant", Content: "last-asst"},
	)
	s := newCompactTestSession(&fakeSummaryLLM{}, 2, frag, frag)
	s.artifacts = mcp.NewArtifactStore()

	if err := s.basicCompact(context.Background()); err != nil {
		t.Fatalf("basicCompact: %v", err)
	}
	got := trimFragment(s)
	if estimateTokens(got) > s.trimBudget(s.compactionConfig()) {
		t.Fatalf("result over budget: %d", estimateTokens(got))
	}
	body := got[0].Content
	if strings.Contains(body, "Assistant: ") {
		t.Fatalf("activity lines must all go before any user line:\n%s", body)
	}
	if strings.Contains(body, "user-0-") {
		t.Fatalf("the oldest user line must be dropped when nothing else fits:\n%s", body)
	}
	if !strings.Contains(body, "user-7-"+strings.Repeat("w", 400)) {
		t.Fatalf("the newest user line must survive verbatim:\n%s", body)
	}
	if art := s.artifacts.Get(1); art == nil || !strings.Contains(art.Content, "user-0-") {
		t.Fatalf("dropped user lines must stay in the artifact")
	}
}

func TestBasicCompactFailsWhenTailAloneOverBudget(t *testing.T) {
	frag := []openai.ChatCompletionMessage{
		{Role: "user", Content: "u1" + strings.Repeat("x", 800)},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: strings.Repeat("y", 4000)},
	}
	s := newCompactTestSession(&fakeSummaryLLM{}, 1, frag, frag)

	err := s.basicCompact(context.Background())
	if !errors.Is(err, ErrCompactionOverBudget) {
		t.Fatalf("want ErrCompactionOverBudget, got %v", err)
	}
	if got := trimFragment(s); len(got) != len(frag) {
		t.Fatalf("a failed basic compact must leave the fragment alone, got %d messages", len(got))
	}
}
