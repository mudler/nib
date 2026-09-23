package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/xlog"

	"github.com/mudler/nib/chat"
)

// suggestTimeout bounds one suggestion request. A late suggestion is worse
// than none: the user has started typing by then.
const suggestTimeout = time.Second

// suggestState is the reply autosuggestion. seq identifies the current wait
// on the user; a tick or result carrying another seq is stale.
type suggestState struct {
	seq   int
	armed bool // the delay is running and no key was pressed yet
	items []chat.Suggestion
}

type suggestTickMsg struct{ seq int }

type suggestResultMsg struct {
	seq   int
	items []chat.Suggestion
	err   error
}

// ghostFor is the grey text to show after input: the rest of the best
// suggestion that starts with input (ignoring case), or the best one when
// input is empty.
func ghostFor(items []chat.Suggestion, input string) string {
	if strings.Contains(input, "\n") {
		return ""
	}
	low := strings.ToLower(input)
	for _, it := range items {
		if len(it.Text) > len(input) && strings.HasPrefix(strings.ToLower(it.Text), low) {
			return it.Text[len(input):]
		}
	}
	return ""
}

// armSuggestion starts the wait for a suggestion after a turn: it fires after
// the session's delay unless a key is pressed first.
func (m *Model) armSuggestion(ok bool) tea.Cmd {
	m.suggest.seq++
	m.suggest.items = nil
	m.suggest.armed = false
	if !ok || m.session == nil || !m.session.SuggestionsEnabled() || m.textarea.Value() != "" {
		return nil
	}
	m.suggest.armed = true
	seq := m.suggest.seq
	return tea.Tick(m.session.SuggestionDelay(), func(time.Time) tea.Msg { return suggestTickMsg{seq: seq} })
}

// fetchSuggestion asks the session for suggestions when tick is still the
// current, untouched wait.
func (m *Model) fetchSuggestion(tick suggestTickMsg) tea.Cmd {
	if tick.seq != m.suggest.seq || !m.suggest.armed || m.loading || m.session == nil {
		return nil
	}
	m.suggest.armed = false
	s, ctx, seq := m.session, m.ctx, tick.seq
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, suggestTimeout)
		defer cancel()
		items, err := s.Suggest(ctx)
		return suggestResultMsg{seq: seq, items: items, err: err}
	}
}

// takeSuggestion stores a result that is still wanted.
func (m *Model) takeSuggestion(msg suggestResultMsg) {
	if msg.err != nil {
		xlog.Debug("reply suggestion failed", "error", msg.err)
		return
	}
	if msg.seq != m.suggest.seq || m.loading {
		return
	}
	m.suggest.items = msg.items
}

// suggestionGhost is the grey text the composer shows now, if any.
func (m Model) suggestionGhost() string {
	if len(m.suggest.items) == 0 || m.loading || m.awaitingApproval || m.awaitingAsk || m.completion.active {
		return ""
	}
	return ghostFor(m.suggest.items, m.textarea.Value())
}

// acceptSuggestion completes the composer to the full suggestion, as if the
// user had typed it. It reports whether there was one to accept.
func (m *Model) acceptSuggestion() bool {
	g := m.suggestionGhost()
	if g == "" {
		return false
	}
	m.textarea.SetValue(m.textarea.Value() + g)
	m.textarea.CursorEnd()
	return true
}

// composerView is the composer with the suggestion's grey text after what
// the user typed. It falls back to the plain composer when the cursor is not
// at the end or the text would not fit on the line.
func (m Model) composerView() string {
	g := m.suggestionGhost()
	if g == "" {
		return m.textarea.View()
	}
	value := m.textarea.Value()
	if value == "" {
		// The textarea already draws its placeholder as grey text with the
		// cursor on its first character: exactly the look wanted.
		ta := m.textarea
		ta.Placeholder = g
		return ta.View()
	}
	li := m.textarea.LineInfo()
	if li.StartColumn+li.CharOffset != len([]rune(value)) ||
		len([]rune(value))+len([]rune(g)) >= m.textarea.Width() {
		return m.textarea.View()
	}
	st := m.textarea.FocusedStyle
	cur := m.textarea.Cursor
	first, rest := []rune(g)[:1], []rune(g)[1:]
	cur.TextStyle = st.Placeholder
	cur.SetChar(string(first))
	line := st.Prompt.Render(m.textarea.Prompt) + st.Text.Render(value) + cur.View() + st.Placeholder.Render(string(rest))
	return st.Base.Render(st.CursorLine.Render(line))
}
