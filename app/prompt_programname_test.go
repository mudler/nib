package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// End to end for the system prompt, over the wire. types covers how the prompt
// is RENDERED from Config; this covers that Options.ProgramName reaches that
// Config, which is the half a compiler cannot check and the half that decides
// what a real model is actually told.
//
// The system prompt is the highest-stakes place the program name appears: the
// model does not quote it, it paraphrases it as advice, so a wrong name here
// becomes an embedded assistant confidently telling a LocalAI user to run a
// binary they do not have.
func TestSystemPromptSentToTheModelNamesTheEmbeddingProgram(t *testing.T) {
	srv, systemPrompt := recordingLLM(t)

	var out bytes.Buffer
	o := pipedOptions(t, srv.URL, "hello", &out)
	o.ProgramName = "local-ai chat"
	runCtx(context.Background(), o)

	got := systemPrompt()
	if got == "" {
		t.Fatal("the model was never sent a system prompt")
	}
	if !strings.Contains(got, "`local-ai chat mcp add") {
		t.Fatalf("the model is being told to run a binary the user does not have:\n%s", got)
	}
	if strings.Contains(got, "nib mcp") || strings.Contains(got, "next nib session") {
		t.Fatalf("the system prompt still names nib:\n%s", got)
	}
}

// The standalone control: with no ProgramName the model gets exactly the prompt
// it has always been given.
func TestSystemPromptSentToTheModelDefaultsToNib(t *testing.T) {
	srv, systemPrompt := recordingLLM(t)

	var out bytes.Buffer
	runCtx(context.Background(), pipedOptions(t, srv.URL, "hello", &out))

	got := systemPrompt()
	if got == "" {
		t.Fatal("the model was never sent a system prompt")
	}
	if !strings.Contains(got, "`nib mcp add <name> -- <command> [args...]`") {
		t.Fatalf("standalone system prompt changed:\n%s", got)
	}
	if !strings.HasSuffix(strings.TrimRight(got, "\n"), "become available on the next nib session.") {
		t.Fatalf("standalone system prompt tail changed:\n%s", got)
	}
}

// recordingLLM is an OpenAI-compatible endpoint that answers once and keeps the
// system message it was sent. The accessor is a closure so the read is
// synchronized with the handler's write.
func recordingLLM(t *testing.T) (*httptest.Server, func() string) {
	t.Helper()
	var mu sync.Mutex
	var system string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &req); err == nil {
			mu.Lock()
			for _, m := range req.Messages {
				if m.Role == "system" && system == "" {
					system = m.Content
				}
			}
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "fake", "object": "chat.completion", "model": "fake",
			"choices": []any{map[string]any{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "hi"},
				"finish_reason": "stop",
			}},
		})
	}))
	t.Cleanup(srv.Close)

	return srv, func() string {
		mu.Lock()
		defer mu.Unlock()
		return system
	}
}
