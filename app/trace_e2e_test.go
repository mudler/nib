package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

// The whole of the observability work in one test: a traced CLI run, driven
// through the real dispatch path, must leave behind both the transcript and a
// spend report. Every piece below it is unit-tested in isolation — the
// preflight, the recorder, the counter, the usage file, the exit summary — and
// none of those would notice if the wiring between them came apart in the one
// process that has to hold them all at once.
func TestTracedCLIRunWritesTranscriptAndUsage(t *testing.T) {
	srv := fakeUsageReportingLLM(t)

	dir := filepath.Join(t.TempDir(), "trace")
	var out, errOut bytes.Buffer
	o := Options{
		BaseDir: t.TempDir(),
		// --cli because the injected non-terminal streams below are refused in
		// any other mode, and because the summary's destination is mode-specific.
		Args: []string{"--cli", "--trace-dir", dir},
		// ApprovalMode "auto": stdin is a buffer that has no answer to give, so
		// an approval prompt would end the run with ExitCodeApprovalNoInput
		// rather than with the completed turn this test is about.
		Defaults:    types.Config{Model: "m", APIKey: "k", BaseURL: srv.URL + "/v1", ApprovalMode: "auto"},
		SkipSetup:   true,
		SkipBareEnv: true,
		Stdin:       strings.NewReader("hello\nexit\n"),
		Stdout:      &out,
		Stderr:      &errOut,
	}
	if code := runCtx(context.Background(), o); code != 0 {
		t.Fatalf("exit code = %d, want 0. stderr: %s", code, errOut.String())
	}

	// Content, not existence. trace.Preflight CREATES a zero-byte trace.ndjson
	// to prove the directory is writable, so an os.Stat here would pass with
	// the recorder entirely unwired — which is the exact failure this test is
	// supposed to catch. What proves a call was recorded is a parseable record
	// for a real chat completion, carrying the response the fake server sent.
	assertRecordedAChatCompletion(t, filepath.Join(dir, "trace.ndjson"))

	data, err := os.ReadFile(filepath.Join(dir, "usage.json"))
	if err != nil {
		t.Fatalf("no usage.json from a traced run: %v", err)
	}
	var u struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		Turns            int `json:"turns"`
	}
	if err := json.Unmarshal(data, &u); err != nil {
		t.Fatalf("usage.json is not valid JSON: %v", err)
	}
	// Non-zero, not merely parseable: a file full of zeroes is what a counter
	// that never sees a call writes, and it is indistinguishable from a correct
	// report of a session that did nothing.
	if u.TotalTokens == 0 {
		t.Fatalf("a completed turn reported no tokens; the counter is not wired to the run: %s", data)
	}
	if u.Turns == 0 {
		t.Fatalf("a completed turn was not counted: %s", data)
	}

	// The summary goes to stderr in CLI mode, never stdout: stdout carries the
	// transcript a caller may be piping into something else.
	if strings.Contains(out.String(), "session:") {
		t.Fatalf("the summary leaked onto stdout: %q", out.String())
	}
	if !strings.Contains(errOut.String(), "session:") {
		t.Fatalf("no summary on stderr: %q", errOut.String())
	}
}

// fakeUsageReportingLLM answers one non-streaming chat completion and reports
// what it charged for it.
//
// Non-streaming is not a simplification: cogito's CLI path does not set
// Callbacks.OnStream, so this is the shape the run actually asks for. The usage
// block is what makes the test mean anything — cogito reads the session's spend
// from it, so a server that omits it leaves the counter legitimately at zero and
// the assertions above would be measuring the fake rather than the wiring.
func fakeUsageReportingLLM(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "cmpl-1",
			"object":  "chat.completion",
			"created": 1,
			"model":   "m",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "done"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{
				"prompt_tokens":     120,
				"completion_tokens": 8,
				"total_tokens":      128,
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// assertRecordedAChatCompletion fails unless the transcript holds at least one
// well-formed NDJSON record for a chat completion that carries both the request
// and the response. Blank lines are tolerated; a malformed one is not, since a
// transcript a consumer cannot parse line by line is a broken transcript.
func assertRecordedAChatCompletion(t *testing.T, path string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("no transcript from a traced run: %v", err)
	}
	defer f.Close()

	var found int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // a request carries the whole history
	for line := 1; sc.Scan(); line++ {
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var rec struct {
			Method   string          `json:"method"`
			Request  json.RawMessage `json:"request"`
			Response *struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			} `json:"response"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(raw, &rec); err != nil {
			t.Fatalf("%s line %d is not valid NDJSON: %v\n%s", path, line, err, raw)
		}
		if rec.Error != "" {
			t.Fatalf("the recorded call failed, so nothing was answered: %s", rec.Error)
		}
		if rec.Method != "chat_completion" || len(rec.Request) == 0 || rec.Response == nil {
			continue
		}
		// The answer itself, not just a choices array: a record whose response
		// came back empty would mean the wrapper recorded the call without ever
		// seeing what the model said.
		if len(rec.Response.Choices) > 0 && rec.Response.Choices[0].Message.Content != "" {
			found++
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if found == 0 {
		// Preflight leaves an empty file behind, so this is the state an
		// untraced run produces too — which is why existence proves nothing.
		t.Fatalf("%s holds no recorded chat completion, so the run was not actually traced", path)
	}
}
