package chat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/types"
	openai "github.com/sashabaranov/go-openai"
)

// Hold the old client's first response until the provider switch has completed.
// This ordering exercises a logical stale write, even with perfectly locked fields.
type delayedOverflowLLM struct {
	overflowLLM
	once    sync.Once
	entered chan struct{}
	release chan struct{}
	err     error
}

func (l *delayedOverflowLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	first := false
	l.once.Do(func() { first = true })
	if first {
		close(l.entered)
		select {
		case <-l.release:
			return cogito.LLMReply{}, cogito.LLMUsage{}, l.err
		case <-ctx.Done():
			return cogito.LLMReply{}, cogito.LLMUsage{}, ctx.Err()
		}
	}
	return l.overflowLLM.CreateChatCompletion(ctx, req)
}

func TestOverflowWindowProviderGeneration(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		kind          OverflowKind
	}{
		{"context", "request (368203 tokens) exceeds the available context size (262144 tokens)", KindContext},
		{"budget", "This model's maximum context length is 262144 tokens. However, you requested 16384 output tokens and your prompt contains 250000 input tokens, for a total of 266384 tokens. Please reduce the length of the input prompt or the number of requested output tokens.", KindBudget},
	} {
		for _, change := range []bool{false, true} {
			name := "same-generation"
			if change {
				name = "same-name-provider-switch"
			}
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				if got := classifyOverflow(errors.New(tc.message)).Kind; got != tc.kind {
					t.Fatalf("overflow kind = %v, want %v", got, tc.kind)
				}
				l := &delayedOverflowLLM{entered: make(chan struct{}), release: make(chan struct{}), err: errors.New(tc.message)}
				s := newOverflowSession(t, l)
				s.compaction.MaxContextTokens = 400000
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				s.ctx = ctx
				done := make(chan error, 1)
				go func() { _, err := s.SendMessage("continue"); done <- err }()
				select {
				case <-l.entered:
				case <-ctx.Done():
					t.Fatal("old turn did not reach the client")
				}
				if change {
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"summary"},"finish_reason":"stop"}]}`))
					}))
					defer srv.Close()
					if err := s.applyProvider(types.ModelProviderConfig{Model: "qwen", BaseURL: srv.URL + "/v1", APIKey: "test"}, "replacement"); err != nil {
						t.Fatal(err)
					}
				}
				close(l.release)
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("turn failed: %v", err)
					}
				case <-ctx.Done():
					t.Fatal("turn did not finish")
				}
				want := 262144
				if change {
					want = 400000
				}
				if got := s.ContextWindow(); got != want {
					t.Fatalf("context window = %d, want %d", got, want)
				}
			})
		}
	}
}
