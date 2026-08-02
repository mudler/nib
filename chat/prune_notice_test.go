package chat

import (
	"testing"

	"github.com/mudler/nib/types"
	openai "github.com/sashabaranov/go-openai"
)

// The manipulator runs on EVERY LLM call. A notice keyed on "something is
// stubbed" rather than "something was just stubbed" would repeat for the rest
// of the session.
func TestPruneNoticeFiresOnlyOnATransition(t *testing.T) {
	var calls []int
	s := &Session{
		pruning: types.ToolOutputPruningConfig{HighWaterTokens: 1000, LowWaterTokens: 1, MinResultTokens: 1},
		callbacks: Callbacks{OnPruneDone: func(results, freed int) {
			calls = append(calls, results)
		}},
	}

	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"a.go"}`), bigResult("c1", 9000),
		callMsg("c2", "read", `{"path":"b.go"}`), bigResult("c2", 9000),
		{Role: "user", Content: "next"},
	}

	s.pruneMessages(msgs)
	if len(calls) != 1 {
		t.Fatalf("first prune fired %d notices, want 1", len(calls))
	}

	s.pruneMessages(msgs)
	s.pruneMessages(msgs)
	if len(calls) != 1 {
		t.Fatalf("notice fired %d times; it must fire only when the stubbed set GROWS", len(calls))
	}
}

// A run that prunes nothing must say nothing.
func TestPruneNoticeSilentWhenNothingIsPruned(t *testing.T) {
	fired := false
	s := &Session{
		pruning:   types.ToolOutputPruningConfig{HighWaterTokens: 24000, LowWaterTokens: 8000, MinResultTokens: 200},
		callbacks: Callbacks{OnPruneDone: func(int, int) { fired = true }},
	}
	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"a.go"}`), bigResult("c1", 100),
		{Role: "user", Content: "next"},
	}
	s.pruneMessages(msgs)
	if fired {
		t.Fatal("a run below the high-water mark fired a prune notice")
	}
}
