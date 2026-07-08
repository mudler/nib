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

func TestHumanizeErrorPassthrough(t *testing.T) {
	if humanizeError(nil) != nil {
		t.Fatalf("nil must pass through as nil")
	}
	orig := errors.New("some unrelated failure")
	if got := humanizeError(orig); got != orig {
		t.Fatalf("unrelated error must be returned unchanged, got %q", got.Error())
	}
}
