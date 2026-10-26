package chat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/mudler/cogito"
	configpkg "github.com/mudler/nib/config"
	"github.com/mudler/nib/types"
	"github.com/sashabaranov/go-openai"
)

// recordingLLM is a cogito.LLM that answers plainly and records the messages of
// every request, so a test can see what the model would have been sent.
type recordingLLM struct {
	mu       sync.Mutex
	requests [][]openai.ChatCompletionMessage
	fail     error // when set, every request fails with it
}

// failAll makes every later request fail with err.
func (r *recordingLLM) failAll(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fail = err
}

func (r *recordingLLM) failure() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fail
}

func (r *recordingLLM) record(msgs []openai.ChatCompletionMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, append([]openai.ChatCompletionMessage(nil), msgs...))
}

func (r *recordingLLM) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	r.record(f.Messages)
	if err := r.failure(); err != nil {
		return f, err
	}
	return f.AddMessage(cogito.AssistantMessageRole, "ok"), nil
}

func (r *recordingLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	r.record(req.Messages)
	if err := r.failure(); err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}
	return replyWith("ok"), cogito.LLMUsage{TotalTokens: 1}, nil
}

func (r *recordingLLM) all() [][]openai.ChatCompletionMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]openai.ChatCompletionMessage(nil), r.requests...)
}

// hostHookConfig builds a config the way an embedding host does: through
// LoadWith, which fills the loop defaults (attempts, retries, compaction).
func hostHookConfig(t *testing.T, llm cogito.LLM) types.Config {
	t.Helper()
	cfg := configpkg.LoadWith(configpkg.LoadOptions{
		BaseDir:     t.TempDir(),
		SkipBareEnv: true,
		Overrides:   types.Config{Model: "host-model", APIKey: "unused"},
	})
	cfg.WorkingDir = t.TempDir()
	cfg.LLM = llm
	return cfg
}

func TestInProcessProvider(t *testing.T) {
	llm := &recordingLLM{}
	s, err := NewSession(context.Background(), hostHookConfig(t, llm), Callbacks{})
	if err != nil {
		t.Fatalf("NewSession with a host LLM: %v", err)
	}
	defer s.Close()
	if _, err := s.SendMessage("hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	reqs := llm.all()
	if len(reqs) == 0 {
		t.Fatal("the host LLM received no request")
	}
	found := false
	for _, m := range reqs[0] {
		if m.Role == "user" && strings.Contains(m.Content, "hello") {
			found = true
		}
	}
	if !found {
		t.Fatalf("first request did not carry the user text: %+v", reqs[0])
	}
}

func TestInputSourceDeliversOneBatchInOrder(t *testing.T) {
	llm := &recordingLLM{}
	cfg := hostHookConfig(t, llm)
	var mu sync.Mutex
	calls, settled := 0, []error{}
	cfg.InputSource = func() ([]string, func(error)) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls > 1 {
			return nil, nil
		}
		return []string{"first extra", "second extra", "third extra"}, func(err error) {
			mu.Lock()
			defer mu.Unlock()
			settled = append(settled, err)
		}
	}
	s, err := NewSession(context.Background(), cfg, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()
	if _, err := s.SendMessage("start"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	var batch []string
	for _, req := range llm.all() {
		for _, m := range req {
			if m.Role == "user" && strings.Contains(m.Content, "first extra") {
				batch = append(batch, m.Content)
			}
		}
	}
	if len(batch) != 1 {
		t.Fatalf("want the extras in exactly one user message, got %d: %q", len(batch), batch)
	}
	a, b, c := strings.Index(batch[0], "first extra"), strings.Index(batch[0], "second extra"), strings.Index(batch[0], "third extra")
	if !(a >= 0 && a < b && b < c) {
		t.Fatalf("extras are missing or out of order: %q", batch[0])
	}
	mu.Lock()
	defer mu.Unlock()
	if len(settled) != 1 || settled[0] != nil {
		t.Fatalf("want one successful settle, got %v", settled)
	}
}

// A batch the model has seen must stay in its history: the next turn, and the
// state a host saves, still hold what the user steered.
func TestInputSourceBatchStaysInTheHistory(t *testing.T) {
	llm := &recordingLLM{}
	cfg := hostHookConfig(t, llm)
	var mu sync.Mutex
	calls := 0
	cfg.InputSource = func() ([]string, func(error)) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls > 1 {
			return nil, nil
		}
		return []string{"steer one", "steer two"}, func(error) {}
	}
	s, err := NewSession(context.Background(), cfg, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()
	if _, err := s.SendMessage("start"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	count := func(msgs []openai.ChatCompletionMessage) int {
		n := 0
		for _, m := range msgs {
			if m.Role == "user" && strings.Contains(m.Content, "steer one") && strings.Contains(m.Content, "steer two") {
				n++
			}
		}
		return n
	}
	if n := count(s.ExportContext()); n != 1 {
		t.Fatalf("the saved context holds the batch %d times, want once", n)
	}
	if _, err := s.SendMessage("and then"); err != nil {
		t.Fatalf("second SendMessage: %v", err)
	}
	all := llm.all()
	if n := count(all[len(all)-1]); n != 1 {
		t.Fatalf("the next turn's request holds the batch %d times, want once", n)
	}
}

// A request that failed did not deliver the batch, so it must not be kept for
// the history: the host puts it back and delivers it again.
func TestInputSourceKeepsABatchOnlyWhenTheRequestWentThrough(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fail     error
		wantKept int
	}{{"went through", nil, 1}, {"failed", errors.New("backend down"), 0}} {
		t.Run(tc.name, func(t *testing.T) {
			llm := &recordingLLM{}
			llm.failAll(tc.fail)
			var kept [][]string
			wrapped := withInputSource(llm, func() ([]string, func(error)) {
				return []string{"extra"}, func(error) {}
			}, func(texts []string) { kept = append(kept, texts) })
			_, _, _ = wrapped.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
				Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "hi"}},
			})
			if len(kept) != tc.wantKept {
				t.Fatalf("kept %d batches, want %d", len(kept), tc.wantKept)
			}
		})
	}
}
