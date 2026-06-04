package chat

import (
	"strings"
	"testing"

	"github.com/mudler/nib/types"
	openai "github.com/sashabaranov/go-openai"
)

func TestSplitForCompactionKeepsToolGroupsIntact(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "assistant", ToolCalls: []openai.ToolCall{{Function: openai.FunctionCall{Name: "ls", Arguments: "{}"}}}},
		{Role: "tool", Content: "tool-result", ToolCallID: "x"},
		{Role: "assistant", Content: "a2"},
	}
	head, tail := splitForCompaction(msgs, 2)
	if len(head) != 2 {
		t.Fatalf("head len = %d, want 2", len(head))
	}
	if tail[0].Role != "assistant" || len(tail[0].ToolCalls) == 0 {
		t.Fatalf("tail must start at the assistant tool_calls message, got %+v", tail[0])
	}
	if len(head) > 0 && len(head[len(head)-1].ToolCalls) > 0 {
		t.Fatal("head must not end on an assistant tool_calls message")
	}
}

func TestSplitForCompactionNothingToCompact(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
	}
	head, tail := splitForCompaction(msgs, 8)
	if head != nil || tail != nil {
		t.Fatalf("expected (nil,nil) when too short, got head=%v tail=%v", head, tail)
	}
}

func TestShouldAutoCompact(t *testing.T) {
	cfg := types.CompactionConfig{MaxContextTokens: 1000, Threshold: 0.8}
	if shouldAutoCompact(cfg, 799) {
		t.Fatal("799 < 800 should not trigger")
	}
	if !shouldAutoCompact(cfg, 800) {
		t.Fatal("800 >= 800 should trigger")
	}
	if shouldAutoCompact(types.CompactionConfig{Disabled: true, MaxContextTokens: 1000, Threshold: 0.8}, 999999) {
		t.Fatal("Disabled must never trigger")
	}
	if shouldAutoCompact(types.CompactionConfig{MaxContextTokens: 0}, 999999) {
		t.Fatal("MaxContextTokens=0 must never trigger")
	}
}

func TestEstimateAndHumanTokens(t *testing.T) {
	got := estimateTokens([]openai.ChatCompletionMessage{{Role: "user", Content: strings.Repeat("a", 40)}})
	if got != 10 { // 40 bytes / 4
		t.Fatalf("estimateTokens = %d, want 10", got)
	}
	if HumanTokens(950) != "950" {
		t.Fatalf("HumanTokens(950) = %q, want 950", HumanTokens(950))
	}
	if HumanTokens(47200) != "47.2k" {
		t.Fatalf("HumanTokens(47200) = %q, want 47.2k", HumanTokens(47200))
	}
}
