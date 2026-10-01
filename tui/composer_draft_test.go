package tui

import (
	"errors"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"
)

func TestComposerPasteLifecycle(t *testing.T) {
	m := newQueueTestModel()
	m.loading = true
	payload := "  " + strings.Repeat("界\n", 3000) + " \r\n"
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("before ")})
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(payload), Paste: true})
	if len(m.textarea.Value()) > 200 {
		t.Fatal("paste not folded")
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" after")})
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	want := "before " + payload + " after"
	if len(m.queue) != 1 || m.queue[0] != want {
		t.Fatal("queue lost source")
	}
	if m.history[0] != want {
		t.Fatal("history lost source")
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyCtrlE})
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.queue[0] != want {
		t.Fatal("queue edit lost source")
	}
}

func TestComposerExactInlinePayload(t *testing.T) {
	for _, payload := range []string{"a\tb", "a\r\nb\r\n", "  界é🙂 \t\r\n ", "one\ntwo", "x\t\r", strings.Repeat("🙂", 1500)} {
		t.Run(payload[:min(len(payload), 16)], func(t *testing.T) {
			m := newQueueTestModel()
			m.loading = true
			m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(payload), Paste: true})
			m = update(m, tea.KeyMsg{Type: tea.KeyLeft})
			if got := m.draft.Expanded(); got != payload {
				t.Fatalf("unrelated key corrupted payload: %q != %q", got, payload)
			}
			m = update(m, tea.KeyMsg{Type: tea.KeyEnter})
			if len(m.queue) != 1 || m.queue[0] != payload {
				t.Fatalf("queue lost exact source: %q", m.queue)
			}
			if m.history[0] != payload {
				t.Fatal("history lost source")
			}
			if got := m.resolveComposer(m.queue[0]).Text; got != payload {
				t.Fatalf("dispatch payload %q", got)
			}
			m = update(m, tea.KeyMsg{Type: tea.KeyCtrlE})
			m = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
			if m.draft.Expanded() != payload {
				t.Fatal("queue restore/resize lost source")
			}
		})
	}
}

func composerKey(m Model, text string) Model {
	return update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
}
func composerPaste(m Model, text string) Model {
	return update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text), Paste: true})
}
func TestComposerMiddleEditsAndLiteralLabel(t *testing.T) {
	m := newQueueTestModel()
	m.loading = true
	m = composerKey(m, "ab")
	m = update(m, tea.KeyMsg{Type: tea.KeyLeft})
	first := strings.Repeat("界\t\r\n", 1100)
	m = composerPaste(m, first)
	m = composerKey(m, "|")
	second := strings.Repeat("β", 1100)
	m = composerPaste(m, second)
	if m.draft.Expanded() != "a"+first+"|"+second+"b" {
		t.Fatal("middle ordering")
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyBackspace}) // whole second paste
	if m.draft.Expanded() != "a"+first+"|b" {
		t.Fatal("atomic backspace")
	}
	m.projectDraft(1)
	m = update(m, tea.KeyMsg{Type: tea.KeyDelete})
	if m.draft.Expanded() != "a|b" {
		t.Fatal("atomic forward delete")
	}
	m.projectDraft(1)
	m = composerPaste(m, "\t界\r\n")
	m = composerKey(m, "!")
	if m.draft.Expanded() != "a\t界\r\n!|b" {
		t.Fatalf("inline edit: %q", m.draft.Expanded())
	}
	m.projectDraft(2) // after the visible tab picture
	m = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.draft.Expanded() != "a界\r\n!|b" {
		t.Fatalf("tab delete: %q", m.draft.Expanded())
	}
	m.clearComposer()
	m = composerPaste(m, second)
	label := m.textarea.Value()
	m = composerKey(m, label)
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.queue[0] != second+label {
		t.Fatal("literal label interpreted")
	}
	m.historyUp()
	if m.draft.Expanded() != second+label {
		t.Fatal("history restore")
	}
}

func TestComposerPanelAndRecovery(t *testing.T) {
	m := newQueueTestModel()
	m.loading = true
	payload := strings.Repeat("字\r\n", 2000)
	m = composerPaste(m, payload)
	open := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p"), Alt: true}
	m = update(m, open)
	if m.pastePanel == nil || m.pastePanel.text != payload {
		t.Fatal("preview source")
	}
	m = composerKey(m, "e")
	m = composerPaste(m, "\t新\r\n")
	m = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.draft.Expanded() != payload {
		t.Fatal("cancel changed source")
	}
	m = update(m, open)
	m = composerKey(m, "e")
	m = composerPaste(m, "\t新\r\n")
	m = update(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	want := "\t新\r\n" + payload
	if m.draft.Expanded() != want {
		t.Fatal("save lost source")
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.draft.Expanded() != "" {
		t.Fatal("clear")
	}
	m.historyUp()
	if m.draft.Expanded() != want {
		t.Fatal("interrupt recovery")
	}
	m = update(m, open)
	m = composerKey(m, "d")
	if m.draft.Expanded() != "" {
		t.Fatal("panel removal")
	}
}

func TestComposerRejectRetainsDraft(t *testing.T) {
	m := newQueueTestModel()
	m.loading = true
	original := "before\t\r\n" + strings.Repeat("z", 5000)
	m = composerPaste(m, original)
	before := m.textarea.Value()
	m = composerPaste(m, strings.Repeat("x", draftMaxBytes))
	if m.draft.Expanded() != original || m.textarea.Value() != before || !strings.Contains(m.hint, "1 MiB") {
		t.Fatal("paste rejection lost draft or error")
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.queue[0] != original {
		t.Fatal("rejected payload submitted")
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyCtrlE})
	open := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p"), Alt: true}
	m = update(m, open)
	m = composerKey(m, "e")
	m = composerPaste(m, strings.Repeat("x", draftMaxBytes))
	if m.pastePanel.text != original || m.draft.Expanded() != original {
		t.Fatal("edit rejection lost draft")
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	m.clearComposer()
	m = composerPaste(m, strings.Repeat("x", draftMaxBytes))
	m = composerKey(m, "!")
	if len(m.draft.Expanded()) != draftMaxBytes || strings.Contains(m.draft.Expanded(), "!") {
		t.Fatal("typing limit not transactional")
	}
}

func TestComposerCompactTranscriptAndFailedCommand(t *testing.T) {
	m := newQueueTestModel()
	payload := " /not-a-command " + strings.Repeat("界\r\n", 2000)
	m = composerPaste(m, payload)
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.messages) == 0 || m.messages[0].Content != payload {
		t.Fatal("transcript source")
	}
	if len(userDisplay(m.messages[0])) > 200 {
		t.Fatal("transcript not compact")
	}
	m.historyUp()
	if m.draft.Expanded() != payload {
		t.Fatal("failed command recovery lost source")
	}
}

func TestComposerOpaqueCaseKeys(t *testing.T) {
	for _, k := range []rune{'u', 'l', 'c'} {
		t.Run(string(k), func(t *testing.T) {
			m := newQueueTestModel()
			m.loading = true
			payload := strings.Repeat("original\n", 8)
			m = composerPaste(m, payload)
			m = update(m, tea.KeyMsg{Type: tea.KeyHome})
			m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{k}, Alt: true})
			if m.draft.Expanded() != payload {
				t.Fatal("case transformation replaced opaque payload")
			}
			m = update(m, tea.KeyMsg{Type: tea.KeyEnter})
			if len(m.queue) != 1 || m.queue[0] != payload {
				t.Fatal("submitted label instead of payload")
			}
		})
	}
}

func TestComposerClipboardBinding(t *testing.T) {
	original := readComposerClipboard
	t.Cleanup(func() { readComposerClipboard = original })
	for _, payload := range []string{"a\tb\r\n", strings.Repeat("a\n", 10001)} {
		readComposerClipboard = func() (string, error) { return payload, nil }
		m := newQueueTestModel()
		m.loading = true
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
		m = next.(Model)
		if cmd == nil {
			t.Fatal("no clipboard command")
		}
		msg := cmd()
		if _, ok := msg.(composerClipboardMsg); !ok {
			t.Fatalf("clipboard bypasses raw route: %T", msg)
		}
		m = update(m, msg)
		if m.draft.Expanded() != payload {
			t.Fatal("clipboard payload lost")
		}
		m = update(m, tea.KeyMsg{Type: tea.KeyEnter})
		if len(m.queue) != 1 || m.queue[0] != payload {
			t.Fatal("clipboard send lost payload")
		}
	}
	m := newQueueTestModel()
	m = composerPaste(m, strings.Repeat("x", 1001))
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}, Alt: true})
	m = composerKey(m, "e")
	payload := "\tRAW\r\n"
	readComposerClipboard = func() (string, error) { return payload, nil }
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("modal clipboard command missing")
	}
	m = update(m, cmd())
	if m.pastePanel.text != payload+strings.Repeat("x", 1001) {
		t.Fatal("modal raw paste lost")
	}
	before := m.pastePanel.text
	readComposerClipboard = func() (string, error) { return "", errors.New("clipboard unavailable") }
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m = next.(Model)
	m = update(m, cmd())
	if m.pastePanel.text != before || !strings.Contains(m.hint, "clipboard unavailable") {
		t.Fatal("modal clipboard error not retained")
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	before = m.draft.Expanded()
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m = next.(Model)
	m = update(m, cmd())
	if m.draft.Expanded() != before || !strings.Contains(m.hint, "clipboard unavailable") {
		t.Fatal("clipboard error not retained")
	}
}

func TestComposerAggregateSmallPastes(t *testing.T) {
	m := newQueueTestModel()
	m.loading = true
	// Equivalent to 10,000 small paste insertions without quadratic test setup.
	snapshot := draftSnapshot{NextID: 10001}
	for i := 1; i <= 10000; i++ {
		snapshot.Segments = append(snapshot.Segments, draftSegmentSnapshot{uint64(i), draftText, "a\n"})
	}
	if err := m.draft.Restore(snapshot); err != nil {
		t.Fatal(err)
	}
	m.projectDraft(len(m.draft.Projection().Text))
	want := strings.Repeat("a\n", 10000)
	if m.textarea.Value() != m.draft.Projection().Text {
		t.Fatal("projection clipped by textarea")
	}
	m = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = update(m, tea.KeyMsg{Type: tea.KeyLeft})
	m = update(m, tea.KeyMsg{Type: tea.KeyEnd})
	m = composerPaste(m, "tail\t\r\n")
	want += "tail\t\r\n"
	if m.draft.Expanded() != want {
		t.Fatal("aggregate update lost payload")
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.queue) != 1 || m.queue[0] != want {
		t.Fatal("aggregate send lost payload")
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyCtrlE})
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	if m.draft.Expanded() != want {
		t.Fatal("aggregate recovery lost payload")
	}
}
