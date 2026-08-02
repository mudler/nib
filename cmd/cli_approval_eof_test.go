package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
)

// fakeToolCallingLLM serves an OpenAI-compatible endpoint that proposes one
// bash call per entry in scripts, in order, and then ends the turn. A single
// piped prompt therefore walks the session straight into an approval prompt.
func fakeToolCallingLLM(t *testing.T, scripts ...string) *httptest.Server {
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

// approvalConfig points a session at the fake LLM with tool approval left in
// its default prompting mode, which is what puts OnToolCall in the path.
func approvalConfig(baseURL string) types.Config {
	return types.Config{
		Model:   "fake-model",
		APIKey:  "fake-key",
		BaseURL: baseURL + "/v1",
		AgentOptions: types.AgentOptions{
			Iterations:  10,
			MaxAttempts: 3,
			MaxRetries:  3,
		},
	}
}

// runApprovalSession drives a whole piped session against the real bash MCP
// server, so "was the tool approved" is answered by whether the command
// actually ran, not by inspecting a mock.
func runApprovalSession(t *testing.T, cfg types.Config, in io.Reader) (error, string) {
	t.Helper()
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	shellJobs := wizmcp.NewShellJobs()
	transports, err := wizmcp.StartTransports(ctx, cfg, shellJobs)
	if err != nil {
		t.Fatalf("StartTransports: %v", err)
	}

	var out, errOut bytes.Buffer
	runErr := RunCLI(ctx, cfg, Streams{In: in, Out: &out, Err: &errOut}, shellJobs, transports...)
	return runErr, out.String()
}

func mustNotExist(t *testing.T, path, transcript string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("the tool call was approved and ran: %s exists\ntranscript:\n%s", path, transcript)
	}
}

// The composed scenario, and the reason this is a safety fix rather than a
// papercut: pipe a prompt in, the agent proposes a shell command, stdin is
// already exhausted, and the approval read returns EOF. The empty string EOF
// yields used to fall through the approval switch to its "type a change" arm,
// which approves, so the command ran unattended and the process exited 0
// looking like a clean success. A closed stdin is not consent.
func TestPipedApprovalDoesNotRunTheToolOnEOF(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	srv := fakeToolCallingLLM(t, "touch "+marker)

	err, out := runApprovalSession(t, approvalConfig(srv.URL), strings.NewReader("please create the file\n"))
	mustNotExist(t, marker, out)
	// Refusing to act is not a success. A script piping a prompt in with
	// stdout discarded has nothing else to go on.
	if !errors.Is(err, ErrApprovalNoInput) {
		t.Fatalf("RunCLI = %v, want ErrApprovalNoInput", err)
	}
	if !strings.Contains(out, theme.CLIDeniedNoInput) {
		t.Fatalf("the transcript does not record why it was denied:\n%s", out)
	}
}

// Denying is only half of it: once stdin is closed the session has to stop
// asking, or a model that keeps proposing tools produces a transcript full of
// questions addressed to nobody.
func TestApprovalStopsPromptingOnceStdinIsClosed(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first")
	second := filepath.Join(dir, "second")
	srv := fakeToolCallingLLM(t, "touch "+first, "touch "+second)

	err, out := runApprovalSession(t, approvalConfig(srv.URL), strings.NewReader("please create the files\n"))
	if !errors.Is(err, ErrApprovalNoInput) {
		t.Fatalf("RunCLI = %v, want ErrApprovalNoInput", err)
	}
	mustNotExist(t, first, out)
	mustNotExist(t, second, out)

	// The prompt is the question itself. It may be asked once, before anyone
	// knows stdin is gone; asking it twice means the second call went looking
	// for an answer on a stream already known to be dead.
	if n := strings.Count(out, theme.CLIApprovePrompt("any bash command")); n > 1 {
		t.Fatalf("prompted %d times after stdin closed:\n%s", n, out)
	}
}

// The other half of "do not collapse the two cases": an empty line typed at a
// live terminal is a deliberate keypress and still means what it always meant,
// which is the free-text arm approving with no adjustment. Only a read that
// failed is treated as no decision.
func TestEmptyLineAtTheApprovalPromptStillApproves(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	srv := fakeToolCallingLLM(t, "touch "+marker)

	// Line 1 is the prompt, line 2 is the empty answer at the approval prompt,
	// "exit" ends the session so nothing here depends on EOF.
	err, out := runApprovalSession(t, approvalConfig(srv.URL), strings.NewReader("please create the file\n\nexit\n"))
	if err != nil {
		t.Fatalf("RunCLI = %v, want nil", err)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("an empty typed line no longer approves: %v\ntranscript:\n%s", statErr, out)
	}
}

// lineThenErrorReader hands back one line and then fails, which is what a
// broken stdin looks like at exactly the wrong moment.
type lineThenErrorReader struct {
	line string
	err  error
	done bool
}

func (r *lineThenErrorReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, r.err
	}
	r.done = true
	n := copy(p, r.line)
	return n, nil
}

// EOF is not the only read that is not a decision. A failed read, and a
// cancelled one, take the same arm: deny. The failure is still reported, so
// unlike EOF this session exits non-zero.
func TestApprovalDeniesWhenTheReadFails(t *testing.T) {
	boom := errors.New("stdin is on fire")
	marker := filepath.Join(t.TempDir(), "executed")
	srv := fakeToolCallingLLM(t, "touch "+marker)

	in := &lineThenErrorReader{line: "please create the file\n", err: boom}
	err, out := runApprovalSession(t, approvalConfig(srv.URL), in)
	mustNotExist(t, marker, out)
	if !errors.Is(err, boom) {
		t.Fatalf("RunCLI = %v, want %v", err, boom)
	}
}

// The asymmetry with the prompt loop, pinned. An answer that arrives together
// with the EOF that ended the stream (`printf 'do X\ny'`, no trailing newline)
// is not treated as an answer here, though the loop goes out of its way to
// honor exactly that shape for a question. Text handed back with the EOF cannot
// be told apart from text that was cut off, and at this prompt anything the
// keywords do not match approves through the free-text arm, so a half-written
// pipe would run the command. Fail closed and let the caller add the newline.
func TestApprovalIgnoresAnAnswerThatArrivesWithTheEOF(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	srv := fakeToolCallingLLM(t, "touch "+marker)

	// No trailing newline after the "y".
	err, out := runApprovalSession(t, approvalConfig(srv.URL), strings.NewReader("please create the file\ny"))
	mustNotExist(t, marker, out)
	if !errors.Is(err, ErrApprovalNoInput) {
		t.Fatalf("RunCLI = %v, want ErrApprovalNoInput", err)
	}
}
