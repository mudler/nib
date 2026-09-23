package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/mudler/nib/chat"
)

func sugg(texts ...string) []chat.Suggestion {
	var out []chat.Suggestion
	for i, t := range texts {
		out = append(out, chat.Suggestion{Text: t, Confidence: 1 - float64(i)/10})
	}
	return out
}

func TestGhostFor(t *testing.T) {
	items := sugg("run the tests", "Commit it", "continue")
	for _, tc := range []struct{ in, want string }{
		{"", "run the tests"},
		{"run", " the tests"},
		{"com", "mit it"}, // case-insensitive prefix, suggestion's own case
		{"COMMIT", " it"},
		{"xyz", ""},
		{"run the tests", ""}, // nothing left to add
		{"run\nthe", ""},
	} {
		if got := ghostFor(items, tc.in); got != tc.want {
			t.Errorf("ghostFor(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := ghostFor(nil, ""); got != "" {
		t.Errorf("no items: %q", got)
	}
}

func suggestModel(t *testing.T) Model {
	m := newQueueTestModel()
	m.ctx = context.Background()
	m.session = classifierSession(t)
	return m
}

func TestTurnEndArmsSuggestion(t *testing.T) {
	m := suggestModel(t)
	seq := m.suggest.seq
	next, cmd := m.Update(responseMsg{content: "Done. Run the tests?"})
	m = next.(Model)
	if !m.suggest.armed || m.suggest.seq == seq || cmd == nil {
		t.Fatalf("armed = %v, seq %d→%d, cmd nil = %v", m.suggest.armed, seq, m.suggest.seq, cmd == nil)
	}
}

func TestFailedTurnDoesNotArm(t *testing.T) {
	for _, err := range []error{context.Canceled, errors.New("backend down")} {
		m := suggestModel(t)
		next, _ := m.Update(responseMsg{err: err})
		if next.(Model).suggest.armed {
			t.Fatalf("armed after %v", err)
		}
	}
}

func TestKeyPressDisarms(t *testing.T) {
	m := suggestModel(t)
	next, _ := m.Update(responseMsg{content: "ok"})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = next.(Model)
	if m.suggest.armed {
		t.Fatal("a key press before the delay must cancel the suggestion")
	}
	if _, cmd := m.Update(suggestTickMsg{seq: m.suggest.seq}); cmd != nil {
		t.Fatal("a disarmed tick still fetched")
	}
}

func TestTickFetchesOnlyForCurrentSeq(t *testing.T) {
	m := suggestModel(t)
	next, _ := m.Update(responseMsg{content: "ok"})
	m = next.(Model)
	if _, cmd := m.Update(suggestTickMsg{seq: m.suggest.seq - 1}); cmd != nil {
		t.Fatal("a stale tick fetched")
	}
	if _, cmd := m.Update(suggestTickMsg{seq: m.suggest.seq}); cmd == nil {
		t.Fatal("the current tick did not fetch")
	}
}

func TestStaleResultDropped(t *testing.T) {
	m := suggestModel(t)
	next, _ := m.Update(responseMsg{content: "ok"})
	m = next.(Model)
	next, _ = m.Update(suggestResultMsg{seq: m.suggest.seq - 1, items: sugg("old")})
	m = next.(Model)
	if len(m.suggest.items) != 0 {
		t.Fatal("stale result kept")
	}
	m.loading = true // a new turn started before the result landed
	next, _ = m.Update(suggestResultMsg{seq: m.suggest.seq, items: sugg("late")})
	m = next.(Model)
	if len(m.suggest.items) != 0 {
		t.Fatal("result kept during a turn")
	}
	m.loading = false
	next, _ = m.Update(suggestResultMsg{seq: m.suggest.seq, items: sugg("fresh")})
	if got := next.(Model).suggest.items; len(got) != 1 || got[0].Text != "fresh" {
		t.Fatalf("items = %v", got)
	}
}

func TestTabAcceptsSuggestion(t *testing.T) {
	m := suggestModel(t)
	m.suggest.items = sugg("run the tests")
	m.textarea.SetValue("run")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	if m.textarea.Value() != "run the tests" {
		t.Fatalf("composer = %q", m.textarea.Value())
	}
	if m.loading || len(m.messages) != 0 {
		t.Fatal("Tab sent the suggestion")
	}
	_ = cmd
	if g := m.suggestionGhost(); g != "" {
		t.Fatalf("ghost after accepting = %q", g)
	}
}

func TestCompletionPopupHidesSuggestion(t *testing.T) {
	m := suggestModel(t)
	m.suggest.items = sugg("/approve auto")
	m.textarea.SetValue("/")
	m.completion.setRegistries(nil, nil, nil)
	m.completion.sync("/")
	if !m.completion.active {
		t.Fatal("fixture: popup should be open")
	}
	if g := m.suggestionGhost(); g != "" {
		t.Fatalf("ghost with the popup open = %q", g)
	}
}

func TestNoGhostWhileLoading(t *testing.T) {
	m := suggestModel(t)
	m.suggest.items = sugg("continue")
	m.loading = true
	if g := m.suggestionGhost(); g != "" {
		t.Fatalf("ghost during a turn = %q", g)
	}
}

func TestComposerShowsGhostInline(t *testing.T) {
	m := suggestModel(t)
	m.textarea.SetWidth(60)
	m.suggest.items = sugg("run the tests")
	for _, in := range []string{"", "run"} {
		m.textarea.SetValue(in)
		m.textarea.CursorEnd()
		if got := ansi.Strip(m.composerView()); !strings.Contains(got, "run the tests") {
			t.Fatalf("input %q: composer = %q", in, got)
		}
	}
	m.textarea.SetValue("xyz")
	if got := ansi.Strip(m.composerView()); strings.Contains(got, "the tests") {
		t.Fatalf("ghost shown for a non-matching input: %q", got)
	}
}
