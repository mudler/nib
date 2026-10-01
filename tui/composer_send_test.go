package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

// Exercise Enter's returned command, not just the queue or resolver: the
// authoritative bytes must reach a real session's HTTP provider request.
func TestComposerPasteReachesProvider(t *testing.T) {
	for _, tc := range []struct{ name, payload string }{
		{"large-unicode-controls", "  " + strings.Repeat("界🙂é\t\r\n\x00\x1b\x7f", 1200) + " \r\n"},
		{"inline-multiline", "  first\tline\r\n第二行\n  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var users []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/chat/completions" {
					http.NotFound(w, r)
					return
				}
				var req struct {
					Messages []struct {
						Role    string `json:"role"`
						Content string `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("decode request: %v", err)
					http.Error(w, "bad request", 400)
					return
				}
				mu.Lock()
				for _, msg := range req.Messages {
					if msg.Role == "user" {
						users = append(users, msg.Content)
					}
				}
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"fake","object":"chat.completion","model":"fake-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
			}))
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cfg := types.Config{Model: "fake-model", APIKey: "fake-key", BaseURL: srv.URL + "/v1", BaseDir: t.TempDir(), LogLevel: "error", ApprovalMode: "auto", AgentOptions: types.AgentOptions{Iterations: 2, MaxAttempts: 1, MaxRetries: 1}, Compaction: types.CompactionConfig{MaxContextTokens: 128000}}
			session, err := chat.NewSession(ctx, cfg, chat.Callbacks{})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			m := newQueueTestModel()
			m.ctx, m.cfg, m.session = ctx, cfg, session
			m = composerKey(m, "before ")
			m = composerPaste(m, tc.payload)
			if m.loading || len(m.queue) != 0 || len(m.messages) != 0 {
				t.Fatal("bracketed multiline paste submitted a turn")
			}
			m = composerKey(m, " after")
			want := "before " + tc.payload + " after"
			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(Model)
			if cmd == nil {
				t.Fatal("Enter did not return a send command")
			}
			response, ok := cmd().(responseMsg)
			if !ok || response.err != nil {
				t.Fatalf("send response: %+v (responseMsg=%v)", response, ok)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(users) != 1 {
				t.Fatalf("provider received %d user messages, want 1", len(users))
			}
			if users[0] != want {
				t.Fatalf("provider payload differs: got %d bytes, want %d", len(users[0]), len(want))
			}
			if len(m.history) != 1 || m.history[0] != want {
				t.Fatal("send history lost payload")
			}
		})
	}
}
