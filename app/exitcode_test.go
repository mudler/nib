package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mudler/nib/types"
)

// fakeBashProposingLLM serves an OpenAI-compatible endpoint that proposes one
// bash call and then ends the turn, which is all it takes to walk a piped
// session into an approval prompt nobody can answer.
func fakeBashProposingLLM(t *testing.T, scripts ...string) *httptest.Server {
	t.Helper()
	var n int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		msg := map[string]any{"role": "assistant"}
		finish := "stop"
		if i := int(atomic.AddInt64(&n, 1)); i <= len(scripts) {
			args, err := json.Marshal(map[string]string{"script": scripts[i-1]})
			if err != nil {
				t.Errorf("marshal tool arguments: %v", err)
			}
			msg["tool_calls"] = []any{map[string]any{
				"id": "call_bash", "type": "function", "index": 0,
				"function": map[string]any{"name": "bash", "arguments": string(args)},
			}}
			finish = "tool_calls"
		} else {
			msg["content"] = "done"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "fake", "object": "chat.completion", "model": "fake",
			"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// pipedOptions is a one-shot `echo "..." | nib --cli` at the app boundary,
// which is where the exit code lives.
func pipedOptions(t *testing.T, baseURL, prompt string, out io.Writer) Options {
	t.Helper()
	t.Setenv("MODEL", "")
	t.Setenv("API_KEY", "")
	t.Setenv("BASE_URL", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return Options{
		Args:        []string{"--cli"},
		BaseDir:     t.TempDir(),
		SkipSetup:   true,
		SkipBareEnv: true,
		Defaults: types.Config{
			Model:   "fake-model",
			APIKey:  "fake-key",
			BaseURL: baseURL + "/v1",
			AgentOptions: types.AgentOptions{
				Iterations:  10,
				MaxAttempts: 3,
				MaxRetries:  3,
			},
		},
		Stdin:  strings.NewReader(prompt + "\n"),
		Stdout: out,
		Stderr: out,
	}
}

// A piped session that had to refuse a tool call must not look like a session
// that answered the question. Both used to exit 0, so a script with stdout
// discarded could not tell "here is your answer" from "I refused to act", which
// is the same false signal as the EOF that used to be a failure, pointing the
// other way.
//
// The code is its own, not the blanket 1, so it also stays separable from a
// crash or a bad flag.
func TestDeniedForLackOfInputHasItsOwnExitCode(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	srv := fakeBashProposingLLM(t, "touch "+marker)

	var out bytes.Buffer
	code := runCtx(context.Background(), pipedOptions(t, srv.URL, "please create the file", &out))

	if code != ExitCodeApprovalNoInput {
		t.Fatalf("exit code = %d, want %d\ntranscript:\n%s", code, ExitCodeApprovalNoInput, out.String())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("the tool call ran: %s exists\ntranscript:\n%s", marker, out.String())
	}
}

// The control, and the line the new code must not cross: a piped question that
// needed no approval is still a clean success.
func TestPipedQuestionWithNoToolCallStillExitsZero(t *testing.T) {
	srv := fakeBashProposingLLM(t) // no scripts: it answers and stops

	var out bytes.Buffer
	code := runCtx(context.Background(), pipedOptions(t, srv.URL, "just answer me", &out))

	if code != 0 {
		t.Fatalf("exit code = %d, want 0\ntranscript:\n%s", code, out.String())
	}
}
