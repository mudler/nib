package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

// The summary must never be shaped like something a shell would run: in TUI
// mode stdout carries the command the widget inserts, and this guards the
// habit of "just print it" by pinning what the line looks like.
func TestSessionSummaryIsNotShellShaped(t *testing.T) {
	s := chat.FormatSessionSummary(chat.SessionUsage{
		PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110, Turns: 2,
	})
	if !strings.HasPrefix(s, "session:") {
		t.Fatalf("summary lost its identifying prefix: %q", s)
	}
	if strings.Contains(s, "\n") {
		t.Fatalf("summary must stay one line: %q", s)
	}
}

// fakeUsageLLM answers every request with a plain reply and a non-zero usage
// block, which is what puts a real figure on the session counter.
func fakeUsageLLM(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "fake", "object": "chat.completion", "model": "fake",
			"choices": []any{map[string]any{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "done"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{
				"prompt_tokens": 1200, "completion_tokens": 90, "total_tokens": 1290,
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The stream rule, exercised end to end rather than asserted about a string: a
// session that actually spent tokens reports the summary on stderr and leaves
// stdout carrying nothing but the transcript, so `nib --cli > transcript` stays
// clean and a TUI-shaped caller reading stdout never sees it.
func TestRunCLIWritesTheSummaryToStderrOnly(t *testing.T) {
	srv := fakeUsageLLM(t)
	cfg := types.Config{
		Model:   "fake-model",
		APIKey:  "fake-key",
		BaseURL: srv.URL + "/v1",
		AgentOptions: types.AgentOptions{
			Iterations:  10,
			MaxAttempts: 3,
			MaxRetries:  3,
		},
	}

	var out, errOut bytes.Buffer
	if err := RunCLI(context.Background(), cfg, Streams{
		In:  strings.NewReader("say something\nexit\n"),
		Out: &out,
		Err: &errOut,
	}, nil); err != nil {
		t.Fatalf("RunCLI: %v", err)
	}

	if !strings.Contains(errOut.String(), "session:") {
		t.Fatalf("no exit summary on stderr:\nstderr:\n%s\nstdout:\n%s", errOut.String(), out.String())
	}
	if !strings.Contains(errOut.String(), "1 turn") {
		t.Fatalf("the summary does not report the completed turn:\n%s", errOut.String())
	}
	if strings.Contains(out.String(), "session:") {
		t.Fatalf("the summary leaked into stdout, which a caller pipes:\n%s", out.String())
	}
}

// A session that exits before spending anything prints nothing: no summary on
// either stream, rather than a row of zeroes.
func TestRunCLIPrintsNoSummaryForAnUnusedSession(t *testing.T) {
	out, errOut := runCLIScript(t, types.Config{}, "exit\n")

	if strings.Contains(errOut, "session:") {
		t.Fatalf("an unused session printed a summary: %q", errOut)
	}
	if strings.Contains(out, "session:") {
		t.Fatalf("an unused session printed a summary to stdout: %q", out)
	}
}
