package chat

import (
	"errors"
	"strings"
	"testing"
)

func TestHumanizeErrorContextOverflow(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{
			name: "llama.cpp/LocalAI phrasing",
			raw:  "failed to select tool: failed to pick tool: tool selection failed: failed to make a decision after 3 attempts: rpc error: code = Internal desc = request (9739 tokens) exceeds the available context size (8192 tokens), try increasing it",
		},
		{
			name: "OpenAI phrasing (counts reversed)",
			raw:  "This model's maximum context length is 8192 tokens. However, your messages resulted in 9739 tokens.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := errors.New(tc.raw)
			got := humanizeError(orig)

			if got == orig {
				t.Fatalf("expected error to be humanized, got original")
			}
			msg := got.Error()
			if !strings.Contains(msg, "context window") {
				t.Fatalf("message not humanized: %q", msg)
			}
			// Regardless of the order the backend printed the counts, the
			// request size (larger) and model limit (smaller) must be correct.
			if !strings.Contains(msg, "needs ~9739 tokens") || !strings.Contains(msg, "model allows 8192") {
				t.Fatalf("token counts wrong in %q", msg)
			}
			// Original stays reachable for errors.Is/As.
			if !errors.Is(got, orig) {
				t.Fatalf("Unwrap chain broken; errors.Is(got, orig) = false")
			}
		})
	}
}

// The spec's commitment: when the retry ALSO fails, the error says compaction
// was already attempted, so the user is not advised to do the thing nib just
// did. The first overflow keeps the original advice, which is still correct
// there — this is one test so the two texts cannot drift apart unnoticed.
func TestHumanizeTurnErrorAfterCompaction(t *testing.T) {
	raw := "request (9739 tokens) exceeds the available context size (8192 tokens), try increasing it"
	orig := errors.New(raw)

	t.Run("first overflow still advises clearing", func(t *testing.T) {
		msg := humanizeTurnError(orig, false).Error()
		if !strings.Contains(msg, `clear the conversation ("clear")`) {
			t.Fatalf("the first overflow dropped advice that is still correct: %q", msg)
		}
		if strings.Contains(msg, "compacting") {
			t.Fatalf("the first overflow claims a compaction that never happened: %q", msg)
		}
	})

	t.Run("after a failed retry", func(t *testing.T) {
		got := humanizeTurnError(orig, true)
		msg := got.Error()
		if strings.Contains(msg, "clear the conversation") {
			t.Fatalf("the user is told to clear a conversation nib already compacted: %q", msg)
		}
		if !strings.Contains(msg, "compacting the conversation and retrying") {
			t.Fatalf("the message does not say compaction was already attempted: %q", msg)
		}
		// The figures are the point of the message; the shared helper must feed
		// both texts identically.
		if !strings.Contains(msg, "needs ~9739 tokens") || !strings.Contains(msg, "model allows 8192") {
			t.Fatalf("token counts wrong in %q", msg)
		}
		if !errors.Is(got, orig) {
			t.Fatalf("Unwrap chain broken; errors.Is(got, orig) = false")
		}
	})

	t.Run("a non-overflow failure is untouched by the flag", func(t *testing.T) {
		other := errors.New("connection refused")
		if got := humanizeTurnError(other, true); got != other {
			t.Fatalf("unrelated error rewritten as an overflow: %q", got.Error())
		}
	})
}

func TestHumanizeErrorPassthrough(t *testing.T) {
	if humanizeError(nil) != nil {
		t.Fatalf("nil must pass through as nil")
	}
	orig := errors.New("some unrelated failure")
	if got := humanizeError(orig); got != orig {
		t.Fatalf("unrelated error must be returned unchanged, got %q", got.Error())
	}
}

// An empty reply that cogito gave up on after its retries is rewritten into
// something the user can act on. The raw text names cogito internals
// ("streaming decision", finish_reason="") and gives no next step.
func TestHumanizeErrorEmptyReply(t *testing.T) {
	for _, raw := range []string{
		`failed to select tool: failed to pick tool: tool selection failed: failed to make a streaming decision after 3 attempts: streaming decision produced no content (finish_reason="") on attempt 3`,
		`failed to make a decision after 3 attempts: no choices: 0`,
	} {
		orig := errors.New(raw)
		got := humanizeError(orig)
		if got == orig {
			t.Fatalf("not humanized: %q", raw)
		}
		msg := got.Error()
		if !strings.Contains(msg, "empty reply") || !strings.Contains(msg, "/compact") {
			t.Fatalf("message = %q, want it to name the empty reply and suggest /compact", msg)
		}
		if !errors.Is(got, orig) {
			t.Fatal("humanized error must unwrap to the original")
		}
	}
}

// A context overflow keeps its own message even though it also passed through
// the decision retry loop.
func TestHumanizeErrorOverflowWinsOverEmptyReply(t *testing.T) {
	raw := `failed to make a streaming decision after 3 attempts: localai stream: request (9739 tokens) exceeds the available context size (8192 tokens), try increasing it`
	if msg := humanizeError(errors.New(raw)).Error(); !strings.Contains(msg, "context window") {
		t.Fatalf("message = %q, want the context-overflow message", msg)
	}
}
