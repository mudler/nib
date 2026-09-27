package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	openai "github.com/sashabaranov/go-openai"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
	"github.com/mudler/nib/tui/render/inline"
	"github.com/mudler/nib/types"
)

type failingDeleteStore struct {
	*chat.SessionStore
}

func (s *failingDeleteStore) Delete(string) error {
	return errors.New("boom")
}

func TestBuildResumeDialog(t *testing.T) {
	list := &render.SelectList{Items: []string{"a · 1m ago · 2 messages", "b · 2h ago · 5 messages"}, Selected: 1}
	d := buildResumeDialog(list, false)
	if d.Kind != render.DialogResume {
		t.Fatalf("Kind = %v, want DialogResume", d.Kind)
	}
	if d.Title != theme.ResumeTitle {
		t.Errorf("Title = %q, want %q", d.Title, theme.ResumeTitle)
	}
	if len(d.Options) != 2 || d.Options[0].Text != list.Items[0] || d.Options[1].Text != list.Items[1] {
		t.Fatalf("Options mismatch: %+v", d.Options)
	}
	if d.Selected != 1 {
		t.Errorf("Selected = %d, want 1", d.Selected)
	}
	if d.Hint != theme.HelpResume {
		t.Errorf("Hint = %q, want %q", d.Hint, theme.HelpResume)
	}
}

func TestResumeItemsFormatsUntitled(t *testing.T) {
	sessions := []chat.SessionRecord{
		{ID: "a", Title: "fix the bug", Messages: []openai.ChatCompletionMessage{{Role: "user"}, {Role: "assistant"}}},
		{ID: "b", Title: ""},
	}
	items := resumeItems(sessions)
	if !strings.HasPrefix(items[0], "fix the bug · ") || !strings.HasSuffix(items[0], "2 messages") {
		t.Errorf("items[0] = %q", items[0])
	}
	if !strings.HasPrefix(items[1], "(untitled session) · ") {
		t.Errorf("items[1] should fall back to a placeholder title: %q", items[1])
	}
}

func TestStartResumeOpensPickerCwdScoped(t *testing.T) {
	dir := t.TempDir()
	store := chat.NewSessionStore(dir)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	mustSaveTUI(t, store, chat.SessionRecord{ID: "here", Cwd: cwd, Title: "in this dir"})
	mustSaveTUI(t, store, chat.SessionRecord{ID: "elsewhere", Cwd: "/definitely/not/here", Title: "in another dir"})

	m := newTestModel(Model{store: store, textarea: textarea.New(), viewport: viewport.New(80, 20)})
	cmd := m.startResume(false, "")
	if cmd != nil {
		t.Error("startResume should not return a cmd for the picker path")
	}
	if !m.awaitingResume {
		t.Fatal("expected awaitingResume after opening the picker")
	}
	if m.resumeList == nil || len(m.resumeList.Items) != 1 {
		t.Fatalf("expected exactly the cwd-scoped session, got %+v", m.resumeList)
	}
	if len(m.resumeSessions) != 1 || m.resumeSessions[0].ID != "here" {
		t.Fatalf("resumeSessions = %+v, want just the cwd-scoped one", m.resumeSessions)
	}
}

func TestStartResumeClearsDeleteArm(t *testing.T) {
	dir := t.TempDir()
	store := chat.NewSessionStore(dir)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	mustSaveTUI(t, store, chat.SessionRecord{ID: "here", Cwd: cwd, Title: "in this dir"})

	m := newTestModel(Model{
		store:             store,
		textarea:          textarea.New(),
		viewport:          viewport.New(80, 20),
		resumeDeleteArmed: true,
	})
	m.startResume(false, "")
	if !m.awaitingResume {
		t.Fatal("expected awaitingResume after opening the picker")
	}
	if m.resumeDeleteArmed {
		t.Fatal("startResume should clear a stale delete arm")
	}
}

func TestStartResumeAllWidensAcrossCwd(t *testing.T) {
	dir := t.TempDir()
	store := chat.NewSessionStore(dir)
	cwd, _ := os.Getwd()
	mustSaveTUI(t, store, chat.SessionRecord{ID: "here", Cwd: cwd})
	mustSaveTUI(t, store, chat.SessionRecord{ID: "elsewhere", Cwd: "/definitely/not/here"})

	m := newTestModel(Model{store: store, textarea: textarea.New(), viewport: viewport.New(80, 20)})
	m.startResume(true, "")
	if len(m.resumeSessions) != 2 {
		t.Fatalf("--all should list every session regardless of cwd, got %d", len(m.resumeSessions))
	}
}

func TestStartResumeEmptyReportsNotice(t *testing.T) {
	m := newTestModel(Model{store: chat.NewSessionStore(t.TempDir()), textarea: textarea.New(), viewport: viewport.New(80, 20)})
	m.startResume(false, "")
	if m.awaitingResume {
		t.Fatal("an empty store should not open the picker")
	}
	if len(m.messages) != 1 || m.messages[0].Content != theme.ResumeEmpty {
		t.Fatalf("expected the ResumeEmpty notice, got %+v", m.messages)
	}
}

func TestStartResumeWithIDLoadsDirectly(t *testing.T) {
	dir := t.TempDir()
	store := chat.NewSessionStore(dir)
	mustSaveTUI(t, store, chat.SessionRecord{
		ID: "direct", Title: "remembered", Cwd: "/anywhere",
		Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "remember 41"}, {Role: "assistant", Content: "ok"}},
	})

	m := newTestModel(Model{store: store, ctx: context.Background(), textarea: textarea.New(), viewport: viewport.New(80, 20)})
	cmd := m.startResume(false, "direct")
	if cmd == nil {
		t.Fatal("a direct id resume should return a cmd to re-init the session")
	}
	if m.awaitingResume {
		t.Error("a direct id resume should never open the picker")
	}
	if len(m.cfg.InitialHistory) != 2 {
		t.Fatalf("InitialHistory = %+v, want the 2 seeded messages", m.cfg.InitialHistory)
	}
	if m.sessionID != "direct" || m.sessionTitle != "remembered" {
		t.Errorf("session bookkeeping not adopted from the record: id=%q title=%q", m.sessionID, m.sessionTitle)
	}
}

// TestResumeRestoresTheVisibleTranscript is the I4 regression: applyResume
// seeded cfg.InitialHistory (what the MODEL sees) and appended a "restored N
// messages" notice, but never repopulated m.messages (what the USER sees). A
// resumed session landed on an empty screen claiming it had restored a
// conversation that was nowhere on it.
func TestResumeRestoresTheVisibleTranscript(t *testing.T) {
	dir := t.TempDir()
	store := chat.NewSessionStore(dir)
	mustSaveTUI(t, store, chat.SessionRecord{
		ID: "direct", Title: "remembered", Cwd: "/anywhere",
		Messages: []openai.ChatCompletionMessage{
			{Role: "system", Content: "you are a helpful agent"},
			{Role: "user", Content: "remember 41"},
			{Role: "assistant", Content: "noted: 41"},
			{Role: "assistant", ToolCalls: []openai.ToolCall{{
				Function: openai.FunctionCall{Name: "bash", Arguments: `{"command":"ls -la"}`},
			}}},
			{Role: "tool", Content: "total 0"},
			{Role: "user", Content: "what was it?"},
		},
	})

	m := newTestModel(Model{
		store: store, ctx: context.Background(),
		textarea: textarea.New(), viewport: viewport.New(80, 20),
		width: 80, height: 24,
	})
	m.startResume(false, "direct")

	// What the user sees, not just what the model was handed.
	var roles []string
	for _, msg := range m.messages {
		roles = append(roles, msg.Role)
	}
	want := []string{"user", "assistant", "tool", "user", "agent"} // + the restored notice
	if len(roles) != len(want) {
		t.Fatalf("restored transcript roles = %v, want %v", roles, want)
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("restored transcript roles = %v, want %v", roles, want)
		}
	}
	if m.messages[0].Content != "remember 41" || m.messages[1].Content != "noted: 41" {
		t.Errorf("restored transcript lost its content: %+v", m.messages[:2])
	}
	if m.messages[2].Name != "bash" {
		t.Errorf("restored tool call lost its name: %+v", m.messages[2])
	}

	// And it reaches the screen, not just the slice.
	m.updateViewport()
	out := m.viewport.View()
	for _, want := range []string{"remember 41", "noted: 41", "what was it?"} {
		if !strings.Contains(out, want) {
			t.Errorf("restored transcript is not on screen: %q missing from %q", want, out)
		}
	}
}

// TestResumeReplacesAnyEarlierTranscript: picking a session mid-conversation
// shows that session, not this one with the other one's messages tacked on.
func TestResumeReplacesAnyEarlierTranscript(t *testing.T) {
	dir := t.TempDir()
	store := chat.NewSessionStore(dir)
	mustSaveTUI(t, store, chat.SessionRecord{
		ID: "direct", Cwd: "/anywhere",
		Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "the stored one"}},
	})

	m := newTestModel(Model{
		store: store, ctx: context.Background(),
		textarea: textarea.New(), viewport: viewport.New(80, 20),
	})
	m = withMessages(m, ChatMessage{Role: "user", Content: "the live one"})
	m.startResume(false, "direct")

	for _, msg := range m.messages {
		if strings.Contains(msg.Content, "the live one") {
			t.Fatalf("the pre-resume transcript survived into the restored one: %+v", m.messages)
		}
	}
}

func TestStartResumeWithBadIDReportsError(t *testing.T) {
	m := newTestModel(Model{store: chat.NewSessionStore(t.TempDir()), textarea: textarea.New(), viewport: viewport.New(80, 20)})
	cmd := m.startResume(false, "nope")
	if cmd != nil {
		t.Error("a load failure should not return a re-init cmd")
	}
	if len(m.messages) != 1 || m.messages[0].Role != "error" {
		t.Fatalf("expected an error notice, got %+v", m.messages)
	}
}

// TestResumeDialogKeyboardNavigation: arrow keys move the picker's selection
// and Enter loads the highlighted session — the same up/down/enter idiom as
// the ask_user dialog (see TestAskDialogKeyboardSelection), reusing
// handleListDialogKey rather than a second copy of the wiring.
func TestResumeDialogKeyboardNavigation(t *testing.T) {
	sessions := []chat.SessionRecord{
		{ID: "alpha", Title: "alpha convo", Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "a"}}},
		{ID: "beta", Title: "beta convo", Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "b"}}},
	}
	m := newTestModel(Model{
		textarea:       textarea.New(),
		viewport:       viewport.New(80, 20),
		width:          80,
		awaitingResume: true,
		resumeSessions: sessions,
		resumeList:     &render.SelectList{Items: resumeItems(sessions)},
		presenter:      testPresenter(),
	})

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	nm := next.(Model)
	if nm.resumeList.Selected != 1 {
		t.Fatalf("Down did not move the selection: %+v", nm.resumeList)
	}
	if cmd != nil {
		t.Error("navigation should not itself return a cmd")
	}

	next, cmd = nm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	nm = next.(Model)
	if nm.awaitingResume {
		t.Error("awaitingResume still set after picking")
	}
	if nm.sessionID != "beta" {
		t.Errorf("sessionID = %q, want beta (the Down-selected row)", nm.sessionID)
	}
	if len(nm.cfg.InitialHistory) != 1 || nm.cfg.InitialHistory[0].Content != "b" {
		t.Fatalf("InitialHistory not seeded from the selected session: %+v", nm.cfg.InitialHistory)
	}
	if cmd == nil {
		t.Error("picking a session should return the re-init cmd")
	}
}

// TestResumeDialogPaging: pgup/pgdn move the picker a window at a time. The
// spec asked for paging and only up/down was ever wired, so SelectList.Page
// had no caller outside its own unit test — a user with forty stored sessions
// arrowed through them one row at a time.
func TestResumeDialogPaging(t *testing.T) {
	sessions := make([]chat.SessionRecord, 40)
	for i := range sessions {
		sessions[i] = chat.SessionRecord{ID: "s" + strconv.Itoa(i), Title: "convo " + strconv.Itoa(i)}
	}
	const window = 8
	m := newTestModel(Model{
		textarea:       textarea.New(),
		viewport:       viewport.New(80, 20),
		width:          80,
		awaitingResume: true,
		resumeSessions: sessions,
		resumeList:     &render.SelectList{Items: resumeItems(sessions), MaxVisible: window},
		presenter:      testPresenter(),
	})

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	nm := next.(Model)
	if nm.resumeList.Selected != window {
		t.Fatalf("PgDown selected row %d, want %d (one window down)", nm.resumeList.Selected, window)
	}
	if cmd != nil {
		t.Error("paging should not itself return a cmd")
	}

	next, _ = nm.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	nm = next.(Model)
	if nm.resumeList.Selected != 0 {
		t.Fatalf("PgUp selected row %d, want 0 (back one window)", nm.resumeList.Selected)
	}

	// Clamping, not wrapping: a page off the top stays on the first row.
	next, _ = nm.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	nm = next.(Model)
	if nm.resumeList.Selected != 0 {
		t.Errorf("PgUp at the top wrapped to row %d, want it clamped at 0", nm.resumeList.Selected)
	}
}

// TestResumeDialogEscCancels proves Esc tears down the picker without
// touching cfg — the same contract ask_user's Esc has via resolveAsk("").
func TestResumeDialogEscCancels(t *testing.T) {
	sessions := []chat.SessionRecord{{ID: "alpha"}}
	m := newTestModel(Model{
		textarea:       textarea.New(),
		viewport:       viewport.New(80, 20),
		awaitingResume: true,
		resumeSessions: sessions,
		resumeList:     &render.SelectList{Items: resumeItems(sessions)},
		presenter:      testPresenter(),
	})

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	nm := next.(Model)
	if nm.awaitingResume || nm.resumeList != nil || nm.resumeSessions != nil {
		t.Fatalf("Esc should clear all picker state: %+v", nm)
	}
	if nm.cfg.InitialHistory != nil {
		t.Error("Esc should not touch cfg.InitialHistory")
	}
	if cmd != nil {
		t.Error("Esc should not return a cmd")
	}
}

// TestResumeDialogSwallowsTyping proves the picker has no free-text fallback
// (unlike ask_user): a character key while it is open does nothing rather
// than landing in the composer.
func TestResumeDialogSwallowsTyping(t *testing.T) {
	sessions := []chat.SessionRecord{{ID: "alpha"}}
	m := newTestModel(Model{
		textarea:       textarea.New(),
		viewport:       viewport.New(80, 20),
		awaitingResume: true,
		resumeSessions: sessions,
		resumeList:     &render.SelectList{Items: resumeItems(sessions)},
		presenter:      testPresenter(),
	})
	m.textarea.Focus()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	nm := next.(Model)
	if strings.TrimSpace(nm.textarea.Value()) != "" {
		t.Errorf("typing while the resume picker is open should be swallowed, got composer = %q", nm.textarea.Value())
	}
	if !nm.awaitingResume {
		t.Error("typing should not close the picker")
	}
}

func mustSaveTUI(t *testing.T, s *chat.SessionStore, rec chat.SessionRecord) {
	t.Helper()
	if err := s.Save(rec); err != nil {
		t.Fatal(err)
	}
}

// TestHelpResumeAdvertisesDelete pins the Task 20 requirement that the delete
// key be advertised in the picker's help copy, not just implemented silently.
// Comparing against fmt.Sprintf("%c delete", resumeDeleteKey) — the actual
// constant Update's keypress guard tests (tui/model.go) — rather than the
// literal "d delete" ties this to the real binding: the original form of
// this test was a copy string checked against a hand-typed copy of itself,
// so renaming resumeDeleteKey without updating this literal would leave a
// green suite advertising the wrong key. TestResumeDeleteKeyPressArms below
// covers the other half: that the advertised key is the one that actually
// arms deletion.
func TestHelpResumeAdvertisesDelete(t *testing.T) {
	want := fmt.Sprintf("%c delete", resumeDeleteKey)
	if !strings.Contains(theme.HelpResume, want) {
		t.Errorf("theme.HelpResume = %q, want it to advertise the delete key as %q", theme.HelpResume, want)
	}
}

// TestResumeDeleteKeyPressArms drives resumeDeleteKey itself (not a
// hand-typed 'd' rune) through Update and confirms it is the key Update's
// guard actually recognizes as the delete-arm trigger — closing the loop
// with TestHelpResumeAdvertisesDelete above so a future rename of
// resumeDeleteKey cannot drift from either the advertised copy or the real
// binding without a test failing somewhere.
func TestResumeDeleteKeyPressArms(t *testing.T) {
	dir := t.TempDir()
	store := chat.NewSessionStore(dir)
	mustSaveTUI(t, store, chat.SessionRecord{ID: "alpha", Cwd: "/p"})
	sessions := []chat.SessionRecord{{ID: "alpha"}}

	m := newTestModel(Model{
		store: store, textarea: textarea.New(), viewport: viewport.New(80, 20), width: 80,
		awaitingResume: true, resumeSessions: sessions,
		resumeList: &render.SelectList{Items: resumeItems(sessions)},
		presenter:  testPresenter(),
	})

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{resumeDeleteKey}})
	nm := next.(Model)
	if !nm.resumeDeleteArmed {
		t.Fatal("pressing resumeDeleteKey should arm the delete confirm")
	}
}

// TestResumeDeletePressOnceArmsWithoutDeleting proves a single 'd' press does
// NOT delete anything: destroying a whole recorded transcript on one
// keypress in a list the user is already navigating with keystrokes is
// exactly the accident shape the Task 20 brief warns about, so the first
// press only arms a confirm.
func TestResumeDeletePressOnceArmsWithoutDeleting(t *testing.T) {
	dir := t.TempDir()
	store := chat.NewSessionStore(dir)
	mustSaveTUI(t, store, chat.SessionRecord{ID: "alpha", Cwd: "/p"})
	mustSaveTUI(t, store, chat.SessionRecord{ID: "beta", Cwd: "/p"})
	sessions := []chat.SessionRecord{{ID: "alpha"}, {ID: "beta"}}

	m := newTestModel(Model{
		store: store, textarea: textarea.New(), viewport: viewport.New(80, 20), width: 80,
		awaitingResume: true, resumeSessions: sessions,
		resumeList: &render.SelectList{Items: resumeItems(sessions)},
		presenter:  testPresenter(),
	})

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	nm := next.(Model)
	if cmd != nil {
		t.Error("arming delete should not itself return a cmd")
	}
	if !nm.awaitingResume || len(nm.resumeSessions) != 2 {
		t.Fatalf("first 'd' press should only arm, not delete: awaitingResume=%v sessions=%d", nm.awaitingResume, len(nm.resumeSessions))
	}
	if !nm.resumeDeleteArmed {
		t.Error("resumeDeleteArmed should be true after the first 'd' press")
	}
	for _, id := range []string{"alpha", "beta"} {
		if _, err := os.Stat(filepath.Join(dir, id+".json")); err != nil {
			t.Errorf("first 'd' press deleted %s.json, want it untouched: %v", id, err)
		}
	}
}

// TestResumeDeleteSecondPressDeletesTheFile proves the second 'd' press (with
// no other key in between) actually deletes the highlighted session's file
// and drops it from the picker's in-memory lists.
func TestResumeDeleteSecondPressDeletesTheFile(t *testing.T) {
	dir := t.TempDir()
	store := chat.NewSessionStore(dir)
	mustSaveTUI(t, store, chat.SessionRecord{ID: "alpha", Cwd: "/p"})
	mustSaveTUI(t, store, chat.SessionRecord{ID: "beta", Cwd: "/p"})
	sessions := []chat.SessionRecord{{ID: "alpha"}, {ID: "beta"}}

	m := newTestModel(Model{
		store: store, textarea: textarea.New(), viewport: viewport.New(80, 20), width: 80,
		awaitingResume: true, resumeSessions: sessions,
		resumeList: &render.SelectList{Items: resumeItems(sessions)}, // Selected == 0 -> "alpha"
		presenter:  testPresenter(),
	})

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	nm := next.(Model)
	next, cmd := nm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	nm = next.(Model)

	if cmd != nil {
		t.Error("deleting should not itself return a cmd")
	}
	if nm.resumeDeleteArmed {
		t.Error("resumeDeleteArmed should be cleared once the delete is applied")
	}
	if len(nm.resumeSessions) != 1 || nm.resumeSessions[0].ID != "beta" {
		t.Fatalf("resumeSessions after delete = %+v, want just beta", nm.resumeSessions)
	}
	if len(nm.resumeList.Items) != 1 {
		t.Fatalf("resumeList.Items after delete = %+v, want 1 entry", nm.resumeList.Items)
	}
	if !nm.awaitingResume {
		t.Error("the picker should stay open — one session remains")
	}
	if _, err := os.Stat(filepath.Join(dir, "alpha.json")); !os.IsNotExist(err) {
		t.Errorf("alpha.json should have been deleted, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "beta.json")); err != nil {
		t.Errorf("beta.json should survive, stat err = %v", err)
	}
}

// TestResumeDeleteFailureLeavesPickerUnchanged proves the picker only updates
// after the backing file delete succeeds; a filesystem failure must not make
// the session disappear from the in-memory list while it still exists on disk.
func TestResumeDeleteFailureLeavesPickerUnchanged(t *testing.T) {
	dir := t.TempDir()
	backing := chat.NewSessionStore(dir)
	mustSaveTUI(t, backing, chat.SessionRecord{ID: "alpha", Cwd: "/p"})
	mustSaveTUI(t, backing, chat.SessionRecord{ID: "beta", Cwd: "/p"})
	sessions := []chat.SessionRecord{{ID: "alpha"}, {ID: "beta"}}

	m := newTestModel(Model{
		store: &failingDeleteStore{SessionStore: backing}, textarea: textarea.New(), viewport: viewport.New(80, 20), width: 80,
		awaitingResume: true, resumeSessions: sessions,
		resumeList: &render.SelectList{Items: resumeItems(sessions)},
		presenter:  testPresenter(),
	})

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	nm := next.(Model)
	next, cmd := nm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	nm = next.(Model)

	if cmd != nil {
		t.Error("deleting should not itself return a cmd")
	}
	if nm.resumeDeleteArmed {
		t.Error("resumeDeleteArmed should be cleared once the delete attempt runs")
	}
	if len(nm.resumeSessions) != 2 || nm.resumeSessions[0].ID != "alpha" || nm.resumeSessions[1].ID != "beta" {
		t.Fatalf("resumeSessions after failed delete = %+v, want alpha/beta unchanged", nm.resumeSessions)
	}
	if len(nm.resumeList.Items) != 2 {
		t.Fatalf("resumeList.Items after failed delete = %+v, want 2 entries", nm.resumeList.Items)
	}
	if !nm.awaitingResume {
		t.Error("the picker should stay open after a failed delete")
	}
	if _, err := os.Stat(filepath.Join(dir, "alpha.json")); err != nil {
		t.Errorf("alpha.json should still exist after failed delete, stat err = %v", err)
	}
}

// TestResumeDeleteAnyOtherKeyCancelsTheArm proves the confirm arms for
// exactly one keypress: pressing something other than 'd' after arming
// cancels the pending delete (and still does its own normal thing — here,
// moving the selection) rather than deleting on some later keypress the user
// no longer intends as a confirm.
func TestResumeDeleteAnyOtherKeyCancelsTheArm(t *testing.T) {
	dir := t.TempDir()
	store := chat.NewSessionStore(dir)
	mustSaveTUI(t, store, chat.SessionRecord{ID: "alpha", Cwd: "/p"})
	mustSaveTUI(t, store, chat.SessionRecord{ID: "beta", Cwd: "/p"})
	sessions := []chat.SessionRecord{{ID: "alpha"}, {ID: "beta"}}

	m := newTestModel(Model{
		store: store, textarea: textarea.New(), viewport: viewport.New(80, 20), width: 80,
		awaitingResume: true, resumeSessions: sessions,
		resumeList: &render.SelectList{Items: resumeItems(sessions)},
		presenter:  testPresenter(),
	})

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	nm := next.(Model)
	if !nm.resumeDeleteArmed {
		t.Fatal("expected armed after the first 'd'")
	}

	next, _ = nm.Update(tea.KeyMsg{Type: tea.KeyDown})
	nm = next.(Model)
	if nm.resumeDeleteArmed {
		t.Error("Down should have cancelled the pending delete, not left it armed")
	}
	if nm.resumeList.Selected != 1 {
		t.Errorf("Down should still move the selection normally: Selected = %d", nm.resumeList.Selected)
	}

	// A THIRD press of 'd' here is a fresh arm on the new row, not a delete —
	// proving the earlier arm was really cancelled and not just relocated.
	next, _ = nm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	nm = next.(Model)
	if !nm.resumeDeleteArmed || len(nm.resumeSessions) != 2 {
		t.Fatalf("'d' after Down should re-arm, not delete: armed=%v sessions=%d", nm.resumeDeleteArmed, len(nm.resumeSessions))
	}
}

// TestResumeDeleteLastSessionClosesPickerCleanly proves the picker degrades
// sanely once a delete empties the list: no crash, no out-of-range
// selection, and awaitingResume clears.
func TestResumeDeleteLastSessionClosesPickerCleanly(t *testing.T) {
	dir := t.TempDir()
	store := chat.NewSessionStore(dir)
	mustSaveTUI(t, store, chat.SessionRecord{ID: "alpha", Cwd: "/p"})
	sessions := []chat.SessionRecord{{ID: "alpha"}}

	m := newTestModel(Model{
		store: store, textarea: textarea.New(), viewport: viewport.New(80, 20), width: 80,
		awaitingResume: true, resumeSessions: sessions,
		resumeList: &render.SelectList{Items: resumeItems(sessions)},
		presenter:  testPresenter(),
	})

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	nm := next.(Model)
	next, _ = nm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	nm = next.(Model)

	if nm.awaitingResume || nm.resumeList != nil || nm.resumeSessions != nil {
		t.Fatalf("deleting the only session should close the picker cleanly: %+v", nm)
	}
	if _, err := os.Stat(filepath.Join(dir, "alpha.json")); !os.IsNotExist(err) {
		t.Errorf("alpha.json should have been deleted, stat err = %v", err)
	}
}

// TestResumeDeleteLastRowClampsSelection proves deleting the LAST row of a
// longer list clamps Selected back into range instead of leaving it pointing
// past the end of the shrunk slice — SelectList.Move wraps and Page clamps,
// neither of which runs as part of a delete, so the delete path must clamp
// itself.
func TestResumeDeleteLastRowClampsSelection(t *testing.T) {
	dir := t.TempDir()
	store := chat.NewSessionStore(dir)
	mustSaveTUI(t, store, chat.SessionRecord{ID: "alpha", Cwd: "/p"})
	mustSaveTUI(t, store, chat.SessionRecord{ID: "beta", Cwd: "/p"})
	sessions := []chat.SessionRecord{{ID: "alpha"}, {ID: "beta"}}

	m := newTestModel(Model{
		store: store, textarea: textarea.New(), viewport: viewport.New(80, 20), width: 80,
		awaitingResume: true, resumeSessions: sessions,
		resumeList: &render.SelectList{Items: resumeItems(sessions), Selected: 1}, // "beta", the last row
		presenter:  testPresenter(),
	})

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	nm := next.(Model)
	next, _ = nm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	nm = next.(Model)

	if len(nm.resumeSessions) != 1 || nm.resumeSessions[0].ID != "alpha" {
		t.Fatalf("resumeSessions after deleting the last row = %+v, want just alpha", nm.resumeSessions)
	}
	if nm.resumeList.Selected < 0 || nm.resumeList.Selected >= len(nm.resumeList.Items) {
		t.Fatalf("Selected = %d out of range for %d items", nm.resumeList.Selected, len(nm.resumeList.Items))
	}
}

// TestBuildResumeDialogArmedShowsConfirmHint proves the dialog's own hint
// line switches to the delete-confirm prompt while armed, distinct from the
// normal HelpResume hint (see buildResumeDialog).
func TestBuildResumeDialogArmedShowsConfirmHint(t *testing.T) {
	list := &render.SelectList{Items: []string{"a · 1m ago · 2 messages"}}
	d := buildResumeDialog(list, true)
	if d.Hint != theme.ResumeDeleteConfirm {
		t.Errorf("armed Hint = %q, want %q", d.Hint, theme.ResumeDeleteConfirm)
	}
	d = buildResumeDialog(list, false)
	if d.Hint != theme.HelpResume {
		t.Errorf("unarmed Hint = %q, want %q", d.Hint, theme.HelpResume)
	}
}

// TestStartupResumeShowsTheTranscript: `nib --resume` resolves the record
// before the TUI exists and hands it over through cfg. The model was already
// seeded from cfg.InitialHistory, but the screen stayed empty and the boot
// line said "new", so a resumed session looked like a fresh one. The startup
// path has to show what /resume shows, and keep the record's Created stamp.
func TestStartupResumeShowsTheTranscript(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	created := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	m := NewModel(ctx, types.Config{
		BaseDir: t.TempDir(),
		InitialHistory: []openai.ChatCompletionMessage{
			{Role: "user", Content: "remember 41"},
			{Role: "assistant", Content: "noted: 41"},
		},
		ResumeSessionID:      "pinned-id",
		ResumeSessionTitle:   "remembered",
		ResumeSessionCreated: created,
	}, 40, nil, inline.New())

	var roles []string
	for _, msg := range m.messages {
		roles = append(roles, msg.Role)
	}
	if want := []string{"user", "assistant", "agent"}; strings.Join(roles, ",") != strings.Join(want, ",") {
		t.Fatalf("startup transcript roles = %v, want %v", roles, want)
	}
	if m.messages[0].Content != "remember 41" || m.messages[1].Content != "noted: 41" {
		t.Errorf("startup transcript lost its content: %+v", m.messages[:2])
	}
	if want := fmt.Sprintf(theme.ResumeRestored, 2); m.messages[2].Content != want {
		t.Errorf("restored notice = %q, want %q", m.messages[2].Content, want)
	}
	if !m.sessionCreated.Equal(created) {
		t.Errorf("sessionCreated = %v, want the record's %v", m.sessionCreated, created)
	}
	if got := bootDetail(&m, bootScriptEntry{ev: "session"}); !strings.HasPrefix(got, "resumed ::") {
		t.Errorf("boot session line = %q, want it to say resumed", got)
	}
}

// TestStartupWithPinnedIDOnlyStaysFresh: --session-id without a record to
// load (a supervisor's create call) is a fresh session under a known id.
func TestStartupWithPinnedIDOnlyStaysFresh(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewModel(ctx, types.Config{BaseDir: t.TempDir(), ResumeSessionID: "pinned-id"}, 40, nil, inline.New())
	if len(m.messages) != 0 {
		t.Errorf("fresh pinned session shows messages: %+v", m.messages)
	}
	if got := bootDetail(&m, bootScriptEntry{ev: "session"}); !strings.HasPrefix(got, "new ::") {
		t.Errorf("boot session line = %q, want it to say new", got)
	}
}

// TestStartupResumeShowsTheTranscriptOnceReady: the boot log owns the body
// until it collapses, and it used to collapse only on the first sent message,
// so a restored transcript stayed hidden behind it. A resumed session has a
// conversation to show as soon as it is ready.
func TestStartupResumeShowsTheTranscriptOnceReady(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewModel(ctx, types.Config{
		BaseDir: t.TempDir(),
		InitialHistory: []openai.ChatCompletionMessage{
			{Role: "user", Content: "remember 41"},
			{Role: "assistant", Content: "noted: 41"},
		},
		ResumeSessionID: "pinned-id",
	}, 40, nil, inline.New())
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	updated, _ = updated.(Model).Update(sessionReadyMsg{})
	m = updated.(Model)

	if !m.boot.collapsed {
		t.Fatal("boot log still covers the restored transcript after READY")
	}
	out := m.View()
	for _, want := range []string{"remember 41", "noted: 41"} {
		if !strings.Contains(out, want) {
			t.Errorf("restored transcript is not on screen: %q missing from\n%s", want, out)
		}
	}
}

// TestFreshStartupKeepsTheBootLog: without a transcript the boot log stays
// up after READY until the first message, as before.
func TestFreshStartupKeepsTheBootLog(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewModel(ctx, types.Config{BaseDir: t.TempDir()}, 40, nil, inline.New())
	updated, _ := m.Update(sessionReadyMsg{})
	if updated.(Model).boot.collapsed {
		t.Fatal("fresh session collapsed its boot log before any message")
	}
}

// TestRestoredTranscriptShowsCompactionAsNotice: the display copy now keeps
// the whole transcript with a notice where each compaction ran. A resume
// shows that notice as nib's own line, not as something the model said.
func TestRestoredTranscriptShowsCompactionAsNotice(t *testing.T) {
	got := restoredTranscript([]openai.ChatCompletionMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "assistant", Name: "nib_compaction_notice", Content: "Compacted 2 earlier messages"},
		{Role: "user", Content: "u2"},
	})
	if len(got) != 4 {
		t.Fatalf("restoredTranscript = %+v, want 4 entries", got)
	}
	if got[2].Role != "agent" || got[2].Content != "Compacted 2 earlier messages" {
		t.Fatalf("compaction notice = %+v, want an agent notice", got[2])
	}
}
