package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/trace"
	openai "github.com/sashabaranov/go-openai"
)

// capturedRequest is the subset of a chat-completions body the model-switch
// tests assert on: the model actually requested, plus the two per-request
// levers NewSession configures on the client (metadata, reasoning effort). A
// SetModel that relabels instead of rebuilding, or that rebuilds without
// re-applying the configuration, shows up here.
type capturedRequest struct {
	Model           string            `json:"model"`
	Metadata        map[string]string `json:"metadata"`
	ReasoningEffort string            `json:"reasoning_effort"`
}

// newLLMServer is a chat-completions endpoint that records every request body
// and answers with a plain, tool-free reply.
func newLLMServer(t *testing.T) (*httptest.Server, func() []capturedRequest) {
	t.Helper()
	var mu sync.Mutex
	var got []capturedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req capturedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
			mu.Lock()
			got = append(got, req)
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]string{"role": "assistant", "content": "ok"},
				"finish_reason": "stop",
			}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, func() []capturedRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]capturedRequest(nil), got...)
	}
}

// askOnce drives one completion through the session's current LLM client so a
// test can observe what that client actually puts on the wire.
func askOnce(t *testing.T, llm cogito.LLM) {
	t.Helper()
	_, _, err := llm.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("CreateChatCompletion: %v", err)
	}
}

func TestSetModelSwapsTheSessionModel(t *testing.T) {
	s := &Session{llmModel: "old-model", baseURL: "http://unused.invalid/v1"}
	s.SetModel("new-model")
	if got := s.Model(); got != "new-model" {
		t.Fatalf("Model() = %q, want new-model", got)
	}
	if s.llm == nil {
		t.Fatal("SetModel left the LLM client nil")
	}
}

// TestSetModelRebuildsTheClient is the property that makes the switch real: the
// next request must go out with the new model name, not merely be labelled with
// it. It also pins that the rebuilt client keeps the per-request configuration
// NewSession applies (metadata, reasoning effort) and the endpoint credentials.
func TestSetModelRebuildsTheClient(t *testing.T) {
	srv, requests := newLLMServer(t)
	s := &Session{
		llmModel:        "old-model",
		apiKey:          "sekrit",
		baseURL:         srv.URL + "/v1",
		metadata:        map[string]string{"enable_thinking": "false"},
		reasoningEffort: "none",
	}

	s.SetModel("new-model")
	if s.llm == nil {
		t.Fatal("SetModel left the LLM client nil")
	}
	askOnce(t, s.llm)

	reqs := requests()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	if reqs[0].Model != "new-model" {
		t.Fatalf("request model = %q, want new-model (the client was relabelled, not rebuilt)", reqs[0].Model)
	}
	if reqs[0].Metadata["enable_thinking"] != "false" {
		t.Fatalf("rebuilt client lost the session metadata: %v", reqs[0].Metadata)
	}
	if reqs[0].ReasoningEffort != "none" {
		t.Fatalf("rebuilt client lost the reasoning effort: %q", reqs[0].ReasoningEffort)
	}
}

// TestSetModelKeepsTracing: a traced session must stay traced across a switch,
// and the transcript must attribute the calls to the new model. Losing the
// wrapper would silently stop recording halfway through a session.
func TestSetModelKeepsTracing(t *testing.T) {
	srv, _ := newLLMServer(t)
	dir := t.TempDir()
	rec, err := trace.NewRecorder(dir)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	t.Cleanup(func() { _ = rec.Close() })

	s := &Session{llmModel: "old-model", baseURL: srv.URL + "/v1", tracer: rec}
	s.SetModel("new-model")
	if s.llm == nil {
		t.Fatal("SetModel left the LLM client nil")
	}
	askOnce(t, s.llm)

	data, err := os.ReadFile(filepath.Join(dir, "trace.ndjson"))
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("nothing was traced after the switch: SetModel dropped the recording wrapper")
	}
	if !strings.Contains(string(data), `"model":"new-model"`) {
		t.Fatalf("transcript does not attribute the call to the new model:\n%s", data)
	}
}

// TestSetModelKeepsHistory is a deliberate difference from a REPL that resets
// the conversation on /model: nib is built around persistent context, and
// /compact exists when history needs trimming.
func TestSetModelKeepsHistory(t *testing.T) {
	s := &Session{llmModel: "old-model", baseURL: "http://unused.invalid/v1"}
	s.messages = []openai.ChatCompletionMessage{
		{Role: "user", Content: "remember the number 41"},
		{Role: "assistant", Content: "noted"},
	}
	s.fragment = cogito.NewFragment(s.messages...)

	s.SetModel("new-model")

	if got := s.ExportHistory(); len(got) != 2 || got[0].Content != "remember the number 41" {
		t.Fatalf("SetModel changed the conversation history: %v", got)
	}
	if got := len(s.fragment.Messages); got != 2 {
		t.Fatalf("SetModel changed the model context: %d messages, want 2", got)
	}
}

// TestSetModelIsFollowedBySubAgents: a sub-agent resolves its model against the
// session model, so switching the session must switch the sub-agents too. Pins
// that the resolution reads the live session model rather than a value captured
// when the session was built.
func TestSetModelIsFollowedBySubAgents(t *testing.T) {
	srv, requests := newLLMServer(t)
	s := &Session{llmModel: "old-model", baseURL: srv.URL + "/v1"}

	s.SetModel("new-model")
	// "" is what spawn_agent sends when the LLM names no model: the sub-agent
	// falls back to the session model, which must now be the new one.
	askOnce(t, s.newAgentLLM(s.Model(), "", 0, nil))

	reqs := requests()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	if reqs[0].Model != "new-model" {
		t.Fatalf("sub-agent model = %q, want new-model (sub-agents did not follow the switch)", reqs[0].Model)
	}
}

// gateLLM parks inside the request until released, so a test can switch the
// model at a point that is provably mid-turn.
type gateLLM struct {
	started chan struct{}
	release chan struct{}
}

func (g *gateLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	select {
	case g.started <- struct{}{}:
	default:
	}
	<-g.release
	return cogito.LLMReply{
		ChatCompletionResponse: openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{{
				Message:      openai.ChatCompletionMessage{Role: "assistant", Content: "ok"},
				FinishReason: openai.FinishReasonStop,
			}},
		},
	}, cogito.LLMUsage{}, nil
}

func (g *gateLLM) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	return f.AddMessage("assistant", "ok"), nil
}

// TestSetModelDuringALiveTurn: turnMu is NOT held for the duration of a turn
// (it only guards turnCancel), so a switch can land at any point inside one.
// The turn must therefore finish on the client it started with, and the switch
// must take effect from the next turn.
func TestSetModelDuringALiveTurn(t *testing.T) {
	s := newWarmTestSession(t)
	s.llmModel = "old-model"
	s.baseURL = "http://unused.invalid/v1"
	g := &gateLLM{started: make(chan struct{}, 1), release: make(chan struct{})}
	s.llm = g

	done := make(chan struct{})
	go func() {
		defer close(done)
		<-g.started
		s.SetModel("new-model")
		close(g.release)
	}()

	resp, err := s.SendMessage("hello")
	<-done
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if resp != "ok" {
		t.Fatalf("response = %q, want ok: the turn did not finish on the client it started with", resp)
	}
	if got := s.Model(); got != "new-model" {
		t.Fatalf("Model() = %q, want new-model", got)
	}
}

// TestSetModelIsSafeUnderConcurrentUse pins the locking. A UI reads the model
// while a turn is running and can switch it from another goroutine, so the
// (client, model name) pair must be swapped atomically with respect to every
// reader. Meaningful under -race.
func TestSetModelIsSafeUnderConcurrentUse(t *testing.T) {
	srv, _ := newLLMServer(t)
	s := newWarmTestSession(t)
	s.llmModel = "old-model"
	s.baseURL = srv.URL + "/v1"

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for range 20 {
			// Warm reads the client the same way a turn does.
			_ = s.Warm(context.Background())
		}
	}()
	go func() {
		defer wg.Done()
		for range 20 {
			_ = s.Model()
		}
	}()
	go func() {
		defer wg.Done()
		for i := range 20 {
			s.SetModel(fmt.Sprintf("model-%d", i))
		}
	}()
	wg.Wait()

	if got := s.Model(); got != "model-19" {
		t.Fatalf("Model() = %q, want model-19", got)
	}
}

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]string{
				{"id": "model-a", "object": "model"},
				{"id": "model-b", "object": "model"},
			},
		})
	}))
	defer srv.Close()

	s := &Session{baseURL: srv.URL + "/v1"}
	got, err := s.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(got) != 2 || got[0] != "model-a" || got[1] != "model-b" {
		t.Fatalf("ListModels = %v, want [model-a model-b]", got)
	}
}

// TestListModelsPropagatesFailure: an endpoint that cannot be listed must
// surface as an error, not as an empty list the UI would render as "no models".
func TestListModelsPropagatesFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"nope"}}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := &Session{baseURL: srv.URL + "/v1"}
	got, err := s.ListModels(context.Background())
	if err == nil {
		t.Fatalf("ListModels returned %v and no error for a failing endpoint", got)
	}
}
