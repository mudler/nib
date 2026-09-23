package tui

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	openai "github.com/sashabaranov/go-openai"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
	"github.com/mudler/xlog"
)

// newSessionID mints an id for a fresh (non-resumed) conversation: a
// sortable timestamp plus a short random suffix to avoid a same-second
// collision. NewModel uses this only when cfg.ResumeSessionID is empty — a
// --resume'd launch keeps the stored session's own id instead (see NewModel).
func newSessionID() string {
	return fmt.Sprintf("%s-%04x", time.Now().Format("20060102-150405"), rand.Intn(0x10000))
}

// firstUserTitle returns the first non-empty user message in hist, truncated
// to a picker-friendly length, or "" if there is none — the /resume list
// falls back to "(untitled session)" for that case (see resumeItems).
func firstUserTitle(hist []openai.ChatCompletionMessage) string {
	for _, msg := range hist {
		if msg.Role != "user" {
			continue
		}
		if content := strings.TrimSpace(msg.Content); content != "" {
			return render.TruncateLine(content, 60)
		}
	}
	return ""
}

// recordSession persists the live conversation to m.store so it can be
// resumed later. Called at every turn boundary (the responseMsg branch of
// Update) and once more from quit() — an autosave, not a user action, so a
// failure here is logged and never surfaces as an error banner or blocks
// exit; the in-memory conversation is unaffected either way.
func (m *Model) recordSession() {
	if m.store == nil || m.session == nil {
		return
	}
	hist := m.session.ExportHistory()
	if len(hist) == 0 {
		return // nothing worth recording yet (e.g. a session that never sent a turn)
	}
	if m.sessionTitle == "" {
		m.sessionTitle = firstUserTitle(hist)
	}
	cwd, _ := os.Getwd()
	rec := chat.SessionRecord{
		ID:         m.sessionID,
		Title:      m.sessionTitle,
		Cwd:        cwd,
		Model:      m.session.Model(),
		Created:    m.sessionCreated,
		Updated:    time.Now(),
		Messages:   hist,
		Goal:       m.session.Goal(),
		GoalPaused: m.session.GoalPaused(),
	}
	if err := m.store.Save(rec); err != nil {
		xlog.Warn("session autosave failed", "id", m.sessionID, "error", err)
	}
}

// resumeItems formats each session as "title · relative-time · n messages"
// for the /resume picker list — see buildResumeDialog. index i of the
// returned slice corresponds to sessions[i], which resolveResumePick indexes
// back into once the user picks a row.
func resumeItems(sessions []chat.SessionRecord) []string {
	items := make([]string, len(sessions))
	for i, s := range sessions {
		title := s.Title
		if title == "" {
			title = "(untitled session)"
		}
		items[i] = fmt.Sprintf("%s · %s · %d messages", title, relativeAge(s.Updated), len(s.Messages))
	}
	return items
}

// relativeAge renders a coarse human-readable age ("3m ago", "2h ago", "5d
// ago") for the /resume picker rows.
func relativeAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// buildResumeDialog turns the /resume picker's live list state into a
// render.Dialog — the same shape buildAskDialog produces for ask_user
// (Title, one DialogOption per visible row, Selected, Hint) — so both
// presenters' existing DialogAsk rendering branch covers DialogResume too
// without a second implementation (see tui/render/inline and
// tui/render/full's Dialog method).
// deleteArmed swaps the dialog's Hint from the normal help line to
// theme.ResumeDeleteConfirm while a delete is pending confirmation (see
// Model.handleResumeDeleteKey) — the same Dialog value, just a different
// prompt beneath the option list.
func buildResumeDialog(list *render.SelectList, deleteArmed bool) render.Dialog {
	hint := theme.HelpResume
	if deleteArmed {
		hint = theme.ResumeDeleteConfirm
	}
	d := render.Dialog{Kind: render.DialogResume, Title: theme.ResumeTitle, Hint: hint}
	start, end := list.Window()
	for i := start; i < end; i++ {
		d.Options = append(d.Options, render.DialogOption{Text: list.Items[i]})
	}
	d.Selected = list.Selected - start
	return d
}

// startResume handles /resume: an explicit id loads that session directly;
// otherwise it lists sessions (cwd-scoped unless all) and opens the picker,
// or reports theme.ResumeEmpty if there is nothing to show. Errors from the
// store (a bad --resume id, a broken sessions directory) are reported as a
// transcript error line rather than silently doing nothing. The nil guard on
// m.store is defensive only — NewModel always sets it — but matches
// recordSession's own guard so the two don't drift.
func (m *Model) startResume(all bool, id string) tea.Cmd {
	if m.store == nil {
		return nil
	}
	if id != "" {
		rec, err := m.store.Load(id)
		if err != nil {
			m.appendMessage(ChatMessage{Role: "error", Content: err.Error()})
			return nil
		}
		return m.applyResume(rec)
	}

	cwd := ""
	if !all {
		cwd, _ = os.Getwd()
	}
	sessions, err := m.store.List(cwd)
	if err != nil {
		m.appendMessage(ChatMessage{Role: "error", Content: err.Error()})
		return nil
	}
	if len(sessions) == 0 {
		m.appendMessage(ChatMessage{Role: "agent", Content: theme.ResumeEmpty})
		return nil
	}

	m.resumeSessions = sessions
	m.resumeList = &render.SelectList{Items: resumeItems(sessions), MaxVisible: 8}
	m.resumeDeleteArmed = false
	m.awaitingResume = true
	m.textarea.Focus()
	m.updateViewport()
	return nil
}

// restoredTranscript maps a stored session's openai history onto the visible
// ChatMessage transcript — the projection /resume rebuilds the screen from.
// It is deliberately not a one-to-one copy of the wire history:
//
//   - system messages are the prompt scaffolding, never shown live, so they
//     are not shown on a resume either.
//   - an assistant message carries prose, tool calls, or both. The prose
//     becomes an assistant entry; each tool call becomes a tool entry with the
//     name and arguments the live path passes to toolLabel, so a restored
//     transcript shows the same one-line call summaries it showed the first
//     time round.
//   - tool-result messages are skipped: live, the result is rendered as the
//     preview attached to its call, and re-showing the raw payload of every
//     historical call would bury the conversation it belongs to.
//
// Roles that arrive empty (an assistant turn that was only tool calls) add no
// entry at all rather than an empty bubble.
func restoredTranscript(hist []openai.ChatCompletionMessage) []ChatMessage {
	var out []ChatMessage
	for _, msg := range hist {
		switch msg.Role {
		case openai.ChatMessageRoleUser:
			if strings.TrimSpace(msg.Content) != "" {
				out = append(out, ChatMessage{Role: "user", Content: msg.Content})
			}
		case openai.ChatMessageRoleAssistant:
			if strings.TrimSpace(msg.Content) != "" {
				out = append(out, ChatMessage{Role: "assistant", Content: msg.Content})
			}
			for _, call := range msg.ToolCalls {
				tm := ChatMessage{
					Role:      "tool",
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
				}
				if c := chat.ArgsFileChange(call.Function.Name, call.Function.Arguments); c != nil {
					setChange(&tm, c)
				}
				out = append(out, tm)
			}
		}
	}
	return out
}

// applyResume seeds cfg.InitialHistory from rec and re-runs initSession() —
// the exact path a fresh launch takes (Init calls it too) — so a resumed
// conversation goes through the SAME sessionReadyMsg plumbing (durable-loop
// reload, listener wiring) instead of a second, drifting copy of it. It also
// adopts rec's id/title/created stamp, so the NEXT autosave (recordSession)
// updates this same stored session file instead of forking a new one, and
// rebuilds the on-screen transcript from the same record (restoredTranscript)
// so the user gets back the conversation the model is getting back.
//
// Pointer receiver: both call sites (dispatchResolved's direct-id case,
// resolveResumePick's picker case) already hold an addressable Model they
// mutate in place, exactly like applyAgentEvent elsewhere in this package.
func (m *Model) applyResume(rec chat.SessionRecord) tea.Cmd {
	m.resetSuggestion()
	if m.session != nil {
		// The new session takes its approval mode from m.cfg, a startup
		// snapshot; carry a runtime /yolo across so resuming does not
		// silently switch prompting back on.
		on := m.session.AutoApprove()
		m.carryAutoApprove = &on
		m.session.Close()
	}
	m.cfg.InitialHistory = rec.Messages
	m.cfg.InitialGoal = rec.Goal
	m.cfg.InitialGoalPaused = rec.GoalPaused
	m.sessionID = rec.ID
	m.sessionTitle = rec.Title
	m.sessionCreated = rec.Created
	m.sessionReady = false
	// Rebuild what the USER sees from the same record the MODEL is being
	// seeded with. Without this the screen stayed empty (or, worse, kept the
	// transcript of the conversation being replaced) under a notice claiming
	// N messages had been restored — N messages only the model could see.
	m.messages = nil
	m.appendMessage(restoredTranscript(rec.Messages)...)
	m.appendMessage(ChatMessage{Role: "agent", Content: fmt.Sprintf(theme.ResumeRestored, len(rec.Messages))})
	m.status = theme.Starting
	m.updateViewportFollow()
	return m.initSession()
}

// resolveResumePick finalizes a picker selection on Enter: it tears down the
// picker state and hands the chosen record to applyResume. Value receiver,
// matching resolveAsk's shape — Update's KeyEnter case returns its
// (tea.Model, tea.Cmd) directly.
func (m Model) resolveResumePick() (tea.Model, tea.Cmd) {
	sessions, idx := m.resumeSessions, m.resumeList.Selected
	m.awaitingResume = false
	m.resumeList = nil
	m.resumeSessions = nil
	if idx < 0 || idx >= len(sessions) {
		m.updateViewport()
		return m, nil
	}
	cmd := (&m).applyResume(sessions[idx])
	return m, cmd
}

// cancelResume tears down the picker state without loading anything — the
// Esc branch of the /resume dialog, wired through handleListDialogKey
// exactly like resolveAsk("") is for ask_user's Esc.
func (m Model) cancelResume() (tea.Model, tea.Cmd) {
	m.awaitingResume = false
	m.resumeList = nil
	m.resumeSessions = nil
	m.updateViewport()
	return m, nil
}

// resumeDeleteKey is the /resume picker's delete key (Task 20) — named so
// the keypress guard in Update and theme.HelpResume's advertised key can
// both be tied to the same identifier instead of two independently
// hand-typed 'd' literals silently drifting apart.
const resumeDeleteKey = 'd'

// handleResumeDeleteKey implements the /resume picker's delete affordance
// (Task 20) as a two-keypress confirm rather than an undo: chat/session-
// store.go has no trash or soft-delete, so Delete removes the file outright
// and there would be nothing left to "undo" against — a fake undo that could
// not actually recover the transcript would be worse than none. The first
// 'd' only arms deletion of the highlighted row (buildResumeDialog swaps in
// theme.ResumeDeleteConfirm so the picker itself says so); a SECOND 'd' with
// nothing else pressed in between performs it. Any other key — including
// Esc, handled by the awaitingResume block in Update that calls this —
// clears resumeDeleteArmed instead of deleting, so an accidental second
// keypress while merely navigating never lands on a row the user only meant
// to look at.
//
// This lives here, called only from the m.awaitingResume branch of Update,
// rather than inside the shared handleListDialogKey below: that helper is
// also used by ask_user, which has nothing to delete, so folding 'd' into it
// would fire the same delete-arming logic there — dead code at best, a
// landmine if ask_user ever gives 'd' its own meaning at worst.
func (m Model) handleResumeDeleteKey() (tea.Model, tea.Cmd) {
	if m.resumeList == nil || len(m.resumeList.Items) == 0 {
		return m, nil
	}
	if m.resumeDeleteArmed {
		m.resumeDeleteArmed = false
		m.deleteResumeSelection()
		return m, nil
	}
	m.resumeDeleteArmed = true
	m.updateViewport()
	return m, nil
}

// deleteResumeSelection removes the highlighted session's stored file (via
// m.store.Delete) and, once that succeeds, drops it from both resumeSessions
// and resumeList so the picker stays aligned with what actually exists on
// disk. A delete failure is logged and leaves the picker unchanged rather than
// claiming success in-memory.
//
// Handles both edge cases CORRECTNESS REQUIREMENTS calls out: an empty list
// after the delete closes the picker instead of leaving a dialog with
// nothing to show, and Selected is clamped back into range when the deleted
// row was the last one (SelectList.Move wraps and Page clamps, but neither
// runs as part of a delete, so this must clamp on its own).
func (m *Model) deleteResumeSelection() {
	if m.resumeList == nil {
		return
	}
	idx := m.resumeList.Selected
	if idx < 0 || idx >= len(m.resumeSessions) {
		return
	}
	id := m.resumeSessions[idx].ID
	if m.store != nil {
		if err := m.store.Delete(id); err != nil {
			xlog.Warn("session delete failed", "id", id, "error", err)
			return
		}
	}
	m.resumeSessions = append(m.resumeSessions[:idx], m.resumeSessions[idx+1:]...)
	m.resumeList.Items = append(m.resumeList.Items[:idx], m.resumeList.Items[idx+1:]...)
	if len(m.resumeSessions) == 0 {
		m.awaitingResume = false
		m.resumeList = nil
		m.resumeSessions = nil
		m.appendMessage(ChatMessage{Role: "agent", Content: theme.ResumeEmpty})
		m.updateViewport()
		return
	}
	if m.resumeList.Selected >= len(m.resumeList.Items) {
		m.resumeList.Selected = len(m.resumeList.Items) - 1
	}
	m.updateViewport()
}

// handleListDialogKey applies the navigation shared by every keyboard-driven
// list dialog: up/down move the selection one row (wrapping), pgup/pgdn move
// it one window's worth (clamping — see SelectList.Page), space toggles a
// multi-select check, and Esc invokes onEsc. Paging is what makes a list of
// forty stored sessions navigable at all; without it the only way down the
// list was one arrow press at a time, and SelectList.Page had no non-test
// caller. It returns handled=false for any other key
// (including Enter), which every call site resolves itself — ask_user
// (tui/model.go) must also accept a free-text answer and hand off to a
// blocking channel, and the /resume picker (below) has no free-text
// fallback at all and re-runs initSession() instead. Generalizing exactly
// this much (Task 11's ask_user list wiring, widened to "a dialog with a
// list") is what lets /resume (Task 15) reuse it instead of a second copy of
// the same Move/Toggle plumbing that would only drift from this one.
//
// Navigation only claims a key when the composer is empty — space and the
// arrows are also ordinary characters someone might be typing — mirroring
// every other composer-guarded shortcut in this file.
func (m Model) handleListDialogKey(msg tea.KeyMsg, list *render.SelectList, onEsc func(Model) (tea.Model, tea.Cmd)) (tea.Model, tea.Cmd, bool) {
	if msg.Type == tea.KeyEsc {
		next, cmd := onEsc(m)
		return next, cmd, true
	}
	if strings.TrimSpace(m.textarea.Value()) != "" {
		return m, nil, false
	}
	switch msg.Type {
	case tea.KeyUp:
		list.Move(-1)
	case tea.KeyDown:
		list.Move(1)
	case tea.KeyPgUp:
		list.Page(-1)
	case tea.KeyPgDown:
		list.Page(1)
	case tea.KeySpace:
		if !list.MultiSelect {
			return m, nil, false
		}
		list.Toggle()
	default:
		return m, nil, false
	}
	m.updateViewport()
	return m, nil, true
}
