package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
)

// Nothing spent yet means nothing to show, matching how contextBadge hides
// itself before the first turn.
func TestUsageBadgeHiddenWhenZero(t *testing.T) {
	m := Model{}
	if got := m.usageBadge(); got != "" {
		t.Fatalf("usageBadge on a fresh session = %q, want empty", got)
	}
}

// Plain words, matching contextBadge's "ctx 8k (6%)" phrasing and the calm
// no-emoji voice TestNoEmojiInRenderHelpers guards.
func TestUsageBadgeFormatsBothDirections(t *testing.T) {
	m := Model{sessionUsage: chat.SessionUsage{PromptTokens: 312000, CompletionTokens: 18400}}
	got := m.usageBadge()
	if !strings.Contains(got, "312k") || !strings.Contains(got, "18.4k") {
		t.Fatalf("usageBadge = %q, want both directions via HumanTokens", got)
	}
	if containsEmoji(got) {
		t.Fatalf("usageBadge emitted emoji: %q", got)
	}
}

// The context badge predicts compaction and is therefore actionable; session
// totals are informational and are dropped WHOLE rather than truncated. ttyd in
// a browser is the reporter's normal case, so this is the common path, not an
// edge case.
func TestNarrowFooterDropsUsageAndKeepsContext(t *testing.T) {
	m := Model{
		width:         30,
		contextTokens: 47200,
		sessionUsage:  chat.SessionUsage{PromptTokens: 312000, CompletionTokens: 18400},
	}
	m.cfg.Compaction.MaxContextTokens = 128000

	got := m.footerBadges(20)
	if strings.Contains(got, "312k") {
		t.Fatalf("usage survived a narrow footer: %q", got)
	}
	if !strings.Contains(got, "ctx") {
		t.Fatalf("the context badge was dropped instead of the usage badge: %q", got)
	}
}

// With room for both, both render.
func TestWideFooterShowsBothBadges(t *testing.T) {
	m := Model{
		width:         120,
		contextTokens: 47200,
		sessionUsage:  chat.SessionUsage{PromptTokens: 312000, CompletionTokens: 18400},
	}
	m.cfg.Compaction.MaxContextTokens = 128000

	got := m.footerBadges(20)
	if !strings.Contains(got, "ctx") || !strings.Contains(got, "312k") {
		t.Fatalf("a wide footer should carry both badges: %q", got)
	}
}

// billingOpenAI is a minimal non-streaming OpenAI-compatible endpoint that
// replies with a plain stop message AND reports token usage. The usage block is
// the whole point: a handler that omitted it would let the refresh test pass
// with the refresh line deleted.
func billingOpenAI() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "fake", "object": "chat.completion", "model": "fake",
			"choices": []any{map[string]any{
				"index": 0, "message": map[string]any{"role": "assistant", "content": "ok"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{
				"prompt_tokens": 120, "completion_tokens": 8, "total_tokens": 128,
			},
		})
	}
}

// newSpentSession returns a real session that has completed a turn against a
// fake backend, so its usage counter is genuinely non-zero.
//
// A real session rather than a hand-set field on purpose: chat's addUsage is
// unexported, so from package tui the only honest way to get a session with
// spend on it is to drive one through SendMessage.
func newSpentSession(t *testing.T) *chat.Session {
	t.Helper()
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	srv := httptest.NewServer(billingOpenAI())
	t.Cleanup(srv.Close)

	s, err := chat.NewSession(context.Background(), types.Config{
		Model:        "fake-model",
		APIKey:       "fake-key",
		BaseURL:      srv.URL + "/v1",
		LogLevel:     "error",
		ApprovalMode: "auto",
		AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 3},
	}, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.SendMessage("hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if s.Usage().TotalTokens == 0 {
		t.Fatal("the fake backend reported no usage; the fixture cannot prove anything")
	}
	return s
}

// The wiring, not the arithmetic: the badge renderers above all build
// Model{sessionUsage: ...} literals, so they would keep passing if every
// m.sessionUsage = m.session.Usage() refresh were deleted and the badge went
// permanently blank in the real TUI. This drives a refresh site through Update
// and asserts on the RETURNED model, which is what actually reaches View.
func TestResponseMsgRefreshesSessionUsage(t *testing.T) {
	m := newQueueTestModel()
	m.session = newSpentSession(t)
	m.loading = true

	next, _ := m.Update(responseMsg{content: "done"})
	nm := next.(Model)

	if nm.sessionUsage.TotalTokens == 0 {
		t.Fatal("sessionUsage is still zero after a finished run: the footer refresh is missing")
	}
	if nm.sessionUsage != m.session.Usage() {
		t.Fatalf("sessionUsage = %+v, want the session's own snapshot %+v",
			nm.sessionUsage, m.session.Usage())
	}
	if nm.usageBadge() == "" {
		t.Fatal("the badge is blank on a model that has spent tokens")
	}
}
