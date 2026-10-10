package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

func attachmentStatusModel(t *testing.T) Model {
	t.Helper()
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	s, err := chat.NewSession(context.Background(), types.Config{Model: "test", BaseURL: server.URL, WorkingDir: t.TempDir()}, chat.Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	m := newWakeupTestModel()
	m.ctx = context.Background()
	m.session = s
	m.toolEvents = newToolEventQueue()
	m.reconcileStatus()
	return m
}

func TestStatusAttachmentPreExecutionSettlement(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		name := "missing-text"
		if blocked {
			name = "all-blocked"
		}
		t.Run(name, func(t *testing.T) {
			m := attachmentStatusModel(t)
			before := m.session.ActivitySnapshot().RootExecutionSequence
			text, file := "hello", filepath.Join(t.TempDir(), "missing.txt")
			if blocked {
				text, file = "", filepath.Join(t.TempDir(), "unsupported.png")
			}
			cmd := m.sendWithAttachmentsDeliveryCmd(text, []string{file}, nil, chat.InputAutomatic)
			m.reconcileStatus()
			if !m.activitySummary(time.Now()).Updating {
				t.Fatal("dispatch did not invalidate readiness")
			}
			result := cmd().(responseMsg)
			if blocked {
				if result.err != nil || len(result.blocked) != 1 {
					t.Fatalf("expected blocked no-op: %+v", result)
				}
			} else if result.err == nil {
				t.Fatal("expected read failure")
			}
			if s := m.session.ActivitySnapshot(); s.RootExecutionSequence != before || !s.ReadyAllowed {
				t.Fatalf("expected pre-execution return: %+v", s)
			}
			next, _ := m.Update(result)
			m = next.(Model)
			for i := 0; i < 3; i++ {
				next, _ = m.Update(statusRefreshMsg{m.statusTickSequence})
				m = next.(Model)
			}
			if got := m.activitySummary(time.Now()); got.Primary != "Ready for input" || !got.CountsKnown || m.statusPending {
				t.Fatalf("settled attachment stuck pending: %+v", got)
			}
		})
	}
}

func TestStatusAttachmentSettlementOwnership(t *testing.T) {
	for _, transition := range []string{"new-attachment", "new-text", "new-invalidation", "reset", "replacement", "duplicate"} {
		t.Run(transition, func(t *testing.T) {
			m := attachmentStatusModel(t)
			file := filepath.Join(t.TempDir(), "missing.txt")
			old := m.sendWithAttachmentsDeliveryCmd("old", []string{file}, nil, chat.InputAutomatic)().(responseMsg)
			switch transition {
			case "reset":
				m.resetSchedules()
			case "replacement":
				replacement := attachmentStatusModel(t)
				m.session = replacement.session
			case "duplicate":
				next, _ := m.Update(old)
				m = next.(Model)
			}
			switch transition {
			case "new-text":
				m.sendMessageDelivery("new", chat.InputAutomatic)
			case "new-invalidation":
				m.invalidateStatus()
			default:
				m.sendWithAttachmentsDeliveryCmd("new", []string{file}, nil, chat.InputAutomatic)
			}
			m.loading = true
			next, _ := m.Update(old)
			m = next.(Model)
			m.reconcileStatus()
			if !m.statusPending || !m.activitySummary(time.Now()).Updating {
				t.Fatal("obsolete result settled newer pending input")
			}
			if !m.loading {
				t.Fatal("obsolete result changed current operation UI")
			}
		})
	}
}

func TestStatusAttachmentObsoleteResult(t *testing.T) {
	for _, transition := range []string{"reset", "replacement", "duplicate"} {
		t.Run(transition, func(t *testing.T) {
			m := attachmentStatusModel(t)
			result := m.sendWithAttachmentsDeliveryCmd("old", []string{filepath.Join(t.TempDir(), "missing.txt")}, nil, chat.InputAutomatic)().(responseMsg)
			switch transition {
			case "reset":
				m.resetSchedules()
			case "replacement":
				replacement := attachmentStatusModel(t)
				m.session = replacement.session
			case "duplicate":
				next, _ := m.Update(result)
				m = next.(Model)
			}
			m.loading = true
			next, _ := m.Update(result)
			m = next.(Model)
			if !m.loading {
				t.Fatal("obsolete result accepted without a newer dispatch")
			}
		})
	}
}

// Follow-ups belong to the enclosing attachment turn, not a replacement turn.
func TestStatusAttachmentFollowupResponse(t *testing.T) {
	for _, route := range []string{"queue", "parked-loop"} {
		t.Run(route, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			reached := make(chan struct{}, 1)
			release := make(chan struct{})
			var roots atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" {
					http.NotFound(w, r)
					return
				}
				var req struct {
					Messages []struct{ Role, Content string }
				}
				json.NewDecoder(r.Body).Decode(&req)
				child := false
				for _, msg := range req.Messages {
					child = child || msg.Role == "user" && msg.Content == "subtask"
				}
				msg := map[string]any{"role": "assistant", "content": "attachment final"}
				if route == "queue" || child {
					select {
					case reached <- struct{}{}:
					default:
					}
					select {
					case <-release:
					case <-ctx.Done():
						return
					}
				} else if roots.Add(1) == 1 {
					msg["content"] = ""
					msg["tool_calls"] = []any{map[string]any{"id": "spawn", "type": "function", "function": map[string]any{"name": "spawn_agent", "arguments": `{"task":"subtask","background":true}`}}}
				}
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "stop"}}})
			}))
			defer server.Close()
			defer cancel()
			parked := make(chan struct{}, 1)
			s, err := chat.NewSession(ctx, types.Config{Model: "test", APIKey: "test", BaseURL: server.URL + "/v1", ApprovalMode: "auto", WorkingDir: t.TempDir(), AgentOptions: types.AgentOptions{Iterations: 8, MaxAttempts: 1, MaxRetries: 1}}, chat.Callbacks{OnParked: func(string) {
				select {
				case parked <- struct{}{}:
				default:
				}
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			m := newWakeupTestModel()
			m.ctx, m.session = ctx, s
			m.toolEvents = newToolEventQueue()
			file := filepath.Join(t.TempDir(), "input.txt")
			if err := os.WriteFile(file, []byte("attachment contents"), 0600); err != nil {
				t.Fatal(err)
			}
			m.loading = true
			cmd := m.sendWithAttachmentsDeliveryCmd("read attachment", []string{file}, nil, chat.InputAutomatic)
			done := make(chan responseMsg, 1)
			go func() { done <- cmd().(responseMsg) }()
			wait := reached
			if route == "parked-loop" {
				wait = parked
			}
			select {
			case <-wait:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if route == "queue" {
				m.queue = []queuedInput{{text: "followup", delivery: chat.InputAccepted}}
				if !m.releaseQueueFront() {
					t.Fatal("followup injection rejected")
				}
			} else {
				m.parked = true
				if cmd := m.dispatchLoop("followup"); cmd != nil || m.parked || len(m.queue) != 0 {
					t.Fatal("parked injection rejected")
				}
			}
			// A non-turn queue entry must be flushed at the response boundary.
			m.queue = append(m.queue, queuedInput{text: "/help", delivery: chat.InputAutomatic})
			close(release)
			var result responseMsg
			select {
			case result = <-done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if result.err != nil {
				t.Fatal(result.err)
			}
			if !s.ActivitySnapshot().ReadyAllowed {
				t.Fatal("turn not authoritatively settled")
			}
			next, followup := m.Update(result)
			m = next.(Model)
			found := false
			for _, msg := range m.messages {
				found = found || msg.Role == "assistant" && msg.Content == "attachment final"
			}
			if !found {
				t.Fatal("final attachment response discarded after successful followup injection")
			}
			if m.loading {
				if !m.statusPending || followup == nil || len(m.redispatch) != 0 {
					t.Fatal("undelivered followup was not redispatched normally")
				}
				next, _ = m.Update(followup())
				m = next.(Model)
			}
			if len(m.queue) != 0 || m.loading || m.parked {
				t.Fatalf("response did not settle UI/queue: loading=%v parked=%v queue=%+v", m.loading, m.parked, m.queue)
			}
			m.reconcileStatus()
			if got := m.activitySummary(time.Now()); got.Primary != "Ready for input" {
				t.Fatalf("settled response not ready: %+v", got)
			}
		})
	}
}
