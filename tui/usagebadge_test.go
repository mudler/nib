package tui

import (
	"strings"
	"testing"

	"github.com/mudler/nib/chat"
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
