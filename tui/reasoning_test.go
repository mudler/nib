package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mudler/nib/tui/render/inline"
	"github.com/mudler/nib/types"
)

// TestNewModelDefaultsReasoningCollapsed pins the actual production default
// (the two tests below construct a raw Model{} literal, which has no
// constructor to apply it, so they set the field explicitly instead).
func TestNewModelDefaultsReasoningCollapsed(t *testing.T) {
	m := NewModel(context.Background(), types.Config{}, 40, nil, inline.New())
	if !m.reasoningCollapsed {
		t.Fatal("NewModel must default reasoningCollapsed to true")
	}
}

func longTrace(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "trace line " + string(rune('a'+i%26))
	}
	return strings.Join(lines, "\n")
}

// TestReasoningCollapsedByDefaultTailsTheTrace: the box must show the newest
// lines, not the oldest — a head-anchored box freezes on the opening words and
// reads as a hang.
func TestReasoningCollapsedByDefaultTailsTheTrace(t *testing.T) {
	// reasoningCollapsed is set explicitly here to mirror NewModel's default
	// (true) — a raw Model{} literal, unlike NewModel, has no constructor to
	// apply that default, so a test exercising "collapsed" behaviour must set
	// it itself, the same way this file's other zero-value fields (viewport,
	// presenter) are all set explicitly rather than relied on.
	m := Model{
		viewport:           viewport.New(80, 20),
		width:              80,
		loading:            true,
		reasoning:          "first\n\nsecond\n\nthird\n\nfourth\n\nfifth\n\nsixth\n\nseventh",
		presenter:          testPresenter(),
		reasoningCollapsed: true,
	}
	if !m.reasoningCollapsed {
		t.Fatal("reasoning must start collapsed")
	}
	m.updateViewport()

	out := m.viewport.View()
	if !strings.Contains(out, "seventh") {
		t.Error("collapsed box does not show the newest line")
	}
	if strings.Contains(out, "first") {
		t.Error("collapsed box is showing the oldest line; it should tail, not head")
	}
}

func TestCtrlRExpandsAndCollapsesReasoning(t *testing.T) {
	// Same note as above: reasoningCollapsed must be set explicitly to
	// exercise "starts collapsed, ctrl+r expands, ctrl+r again collapses" —
	// NewModel's default isn't in play for a raw Model{} literal.
	m := Model{
		viewport:           viewport.New(80, 40),
		textarea:           textarea.New(),
		width:              80,
		loading:            true,
		reasoning:          longTrace(20),
		presenter:          testPresenter(),
		reasoningCollapsed: true,
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if next.(Model).reasoningCollapsed {
		t.Fatal("ctrl+r did not expand the reasoning box")
	}
	again, _ := next.(Model).Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if !again.(Model).reasoningCollapsed {
		t.Fatal("ctrl+r did not collapse the reasoning box again")
	}
}
