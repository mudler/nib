package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/slash"
)

// Draft snapshots follow full source through the existing string-based history
// and queue APIs. Labels are never parsed. Entries are retained, not evicted.
func (m *Model) rememberDraft() string {
	text := m.draft.Expanded()
	if m.draftCopies == nil {
		m.draftCopies = make(map[string]draftSnapshot)
	}
	m.draftCopies[text] = m.draft.Snapshot()
	return text
}
func (m *Model) clearComposer() {
	m.draft = inputDraft{}
	m.composerProjectionInvalid = false
	m.textarea.Reset()
}
func (m *Model) setComposer(text string) {
	var d inputDraft
	var err error
	if s, ok := m.draftCopies[text]; ok {
		err = d.Restore(s)
	} else {
		_, err = d.Replace(0, 0, text, true)
	}
	if err != nil {
		m.hint = err.Error()
		return
	}
	m.draft = d
	m.projectDraft(utf8.RuneCountInString(d.Projection().Text))
}
func (m *Model) composerOffset() int {
	i := m.textarea.LineInfo()
	n, _ := m.draft.Projection().Offset(m.textarea.Line(), i.StartColumn+i.ColumnOffset)
	return n
}
func (m *Model) projectDraft(cursor int) {
	p := m.draft.Projection()
	expandedCursor := p.ExpandedOffset(cursor, true)
	if strings.Count(p.Text, "\n")+1 >= draftProjectionMaxLines {
		m.draft.foldInlineRuns()
		p = m.draft.Projection()
		cursor = p.DisplayOffset(expandedCursor, true)
	}
	text := p.Text
	m.textarea.CharLimit = 0
	m.textarea.SetValue(text)
	// Never interpret widget sanitization/truncation as an intentional edit.
	// Folding is also a lossless fallback if a future widget changes its limits.
	if m.textarea.Value() != text {
		m.draft.foldInlineRuns()
		p = m.draft.Projection()
		text = p.Text
		cursor = p.DisplayOffset(expandedCursor, true)
		m.textarea.SetValue(text)
	}
	m.composerProjectionInvalid = m.textarea.Value() != text
	if m.composerProjectionInvalid {
		m.hint = "cannot display draft; original payload retained"
		return
	}
	// SetValue finishes on the last logical line. Move to the requested row,
	// then set a rune column (never a terminal-cell offset).
	prefix := string([]rune(text)[:min(max(cursor, 0), utf8.RuneCountInString(text))])
	row := strings.Count(prefix, "\n")
	for m.textarea.Line() > row {
		m.textarea.CursorUp()
	}
	col := utf8.RuneCountInString(prefix[strings.LastIndex(prefix, "\n")+1:])
	m.textarea.SetCursor(col)
}

// Adopt direct textarea mutations without moving its cursor. In particular,
// synchronization must not reveal a hidden completion by jumping to line end.
func (m *Model) syncComposer() {
	text := m.textarea.Value()
	if text == m.draft.projection.Text {
		return
	}
	info := m.textarea.LineInfo()
	pos, _ := (draftProjection{Text: text}).Offset(m.textarea.Line(), info.StartColumn+info.ColumnOffset)
	m.applyComposerDisplay(text)
	if m.textarea.Value() == text {
		m.projectDraft(pos)
	}
}

// applyComposerDisplay reconciles ordinary textarea edits as one rune range.
// Deletion touching an opaque span is atomic; replacement text must never
// derive from a transformed display label.
func (m *Model) applyComposerDisplay(text string) {
	if m.composerProjectionInvalid {
		m.projectDraft(0)
		return
	}
	old := m.draft.Projection().Text
	if old == text {
		return
	}
	a, b := []rune(old), []rune(text)
	start := 0
	for start < len(a) && start < len(b) && a[start] == b[start] {
		start++
	}
	ae, be := len(a), len(b)
	for ae > start && be > start && a[ae-1] == b[be-1] {
		ae--
		be--
	}
	if be > start {
		for _, span := range m.draft.projection.Spans {
			if span.Kind == draftPaste && start < span.End && ae > span.Start {
				m.hint = errDraftOpaque.Error()
				m.projectDraft(start)
				return
			}
		}
	}
	cursor, err := m.draft.Replace(start, ae, string(b[start:be]), false)
	if err != nil {
		m.hint = err.Error()
		cursor = start
	}
	m.projectDraft(cursor)
}
func (m *Model) pasteComposer(text string) {
	cursor := m.composerOffset()
	next, err := m.draft.Replace(cursor, cursor, text, true)
	if err != nil {
		m.hint = err.Error()
		return
	}
	m.projectDraft(next)
	m.completion.sync(m.textarea.Value())
	m.reflowLayout()
}

// resolveComposer preserves whitespace on plain messages while leaving slash
// expansion and attachment extraction to the established resolver.
func (m Model) resolveComposer(input string) slash.Action {
	a := slash.Resolve(input, m.cfg.Commands, m.cfg.Skills, m.cfg.Agents)
	if a.Kind == slash.KindSend && len(a.Files) == 0 && !strings.HasPrefix(strings.TrimSpace(input), "/") {
		a.Text = input
	}
	return a
}

type pastePanel struct {
	id      uint64
	text    string
	runes   []rune // cached on edit, never decoded in View
	cursor  int
	editing bool
}

func (m *Model) openPastePanel() {
	pos := m.composerOffset()
	var chosen uint64
	for _, s := range m.draft.Projection().Spans {
		if s.Kind == draftPaste {
			if chosen == 0 {
				chosen = s.ID
			}
			if pos >= s.Start && pos <= s.End {
				chosen = s.ID
				break
			}
		}
	}
	if text, ok := m.draft.Preview(chosen); ok {
		m.pastePanel = &pastePanel{id: chosen, text: text, runes: []rune(text)}
	}
	m.reflowLayout()
}
func (m Model) handlePastePanel(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.pastePanel
	if k.Type == tea.KeyEsc || k.Type == tea.KeyCtrlC {
		m.pastePanel = nil
		m.reflowLayout()
		return m, nil
	}
	if !k.Paste && !p.editing && k.String() == "d" {
		_ = m.draft.RemovePaste(p.id)
		m.pastePanel = nil
		m.projectDraft(0)
		m.reflowLayout()
		return m, nil
	}
	if !k.Paste && !p.editing && k.String() == "e" {
		p.editing = true
		return m, nil
	}
	if !k.Paste && p.editing && k.Type == tea.KeyCtrlS {
		if err := m.draft.EditPaste(p.id, p.text); err != nil {
			m.hint = err.Error()
			return m, nil
		}
		m.pastePanel = nil
		m.projectDraft(0)
		m.reflowLayout()
		return m, nil
	}
	r := p.runes
	switch k.Type {
	case tea.KeyLeft:
		p.cursor = max(0, p.cursor-1)
	case tea.KeyRight:
		p.cursor = min(len(r), p.cursor+1)
	case tea.KeyUp, tea.KeyPgUp:
		p.cursor = max(0, p.cursor-400)
	case tea.KeyDown, tea.KeyPgDown:
		p.cursor = min(len(r), p.cursor+400)
	case tea.KeyHome:
		p.cursor = 0
	case tea.KeyEnd:
		p.cursor = len(r)
	default:
		if p.editing {
			start, end := p.cursor, p.cursor
			text := ""
			switch k.Type {
			case tea.KeyRunes:
				text = string(k.Runes)
			case tea.KeySpace:
				text = " "
			case tea.KeyEnter:
				text = "\n"
			case tea.KeyTab:
				text = "\t"
			case tea.KeyBackspace:
				start = max(0, start-1)
			case tea.KeyDelete:
				end = min(len(r), end+1)
			}
			candidate := string(r[:start]) + text + string(r[end:])
			original, _ := m.draft.Preview(p.id)
			if err := validateDraftText(candidate); err != nil {
				m.hint = err.Error()
			} else if m.draft.bytes-len(original)+len(candidate) > draftMaxBytes {
				m.hint = errDraftTooLarge.Error()
			} else {
				p.text = candidate
				p.runes = []rune(candidate)
				p.cursor = start + utf8.RuneCountInString(text)
			}
		}
	}
	m.reflowLayout()
	return m, nil
}
func (m Model) pastePanelView() string {
	p := m.pastePanel
	// Render only a bounded page; never wrap the hidden payload on redraw.
	r := p.runes
	start := max(0, p.cursor-200)
	end := min(len(r), start+400)
	title := "Paste preview · e edit · d remove · arrows/page keys browse · esc close"
	if p.editing {
		title = "Paste edit · ctrl+s save · esc cancel · arrows/home/end move"
	}
	return title + "\n" + string(r[start:p.cursor]) + "│" + string(r[p.cursor:end])
}

func userDisplay(msg ChatMessage) string {
	if msg.displayContent != "" {
		return msg.displayContent
	}
	return msg.Content
}

// Own the clipboard result: bubbles' private paste message has already crossed
// a lossy textarea boundary by the time ordinary display reconciliation runs.
var readComposerClipboard = clipboard.ReadAll

type composerClipboardMsg struct {
	text string
	err  error
}

func pasteComposerClipboard() tea.Msg {
	text, err := readComposerClipboard()
	return composerClipboardMsg{text, err}
}
