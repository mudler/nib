package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
	"github.com/mudler/nib/types"
)

// newModelSwitchTestModel builds a Model wired to a session whose endpoint
// advertises the given model IDs. The first ID is the session's current model.
// No turn is ever started, so the chat-completions side is never exercised and
// no TUI is launched.
func newModelSwitchTestModel(t *testing.T, ids ...string) Model {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := make([]map[string]string, 0, len(ids))
		for _, id := range ids {
			data = append(data, map[string]string{"id": id, "object": "model"})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
	}))
	t.Cleanup(srv.Close)

	cfg := types.Config{
		Model:   ids[0],
		BaseURL: srv.URL + "/v1",
		// A temp base dir keeps the developer's own provider.json (a saved
		// /login pick) from switching this session off the fake endpoint.
		BaseDir:    t.TempDir(),
		Compaction: types.CompactionConfig{MaxContextTokens: 128000},
	}
	s, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	m := newQueueTestModel()
	m.ctx = context.Background()
	m.cfg = cfg
	m.session = s
	return m
}

func lastMessage(t *testing.T, m Model) ChatMessage {
	t.Helper()
	if len(m.messages) == 0 {
		t.Fatal("no message was posted to the transcript")
	}
	return m.messages[len(m.messages)-1]
}

func TestDispatchModelListPostsTheListing(t *testing.T) {
	m := newModelSwitchTestModel(t, "model-a", "model-b")

	if cmd := m.dispatchResolved("/models"); cmd != nil {
		t.Fatal("/models must not start a turn")
	}
	msg := lastMessage(t, m)
	if msg.Role != "agent" {
		t.Fatalf("listing posted as %q, want agent", msg.Role)
	}
	if !strings.Contains(msg.Content, "* model-a") || !strings.Contains(msg.Content, "  model-b") {
		t.Fatalf("listing = %q, want the current model marked", msg.Content)
	}

	// The transcript renders an "agent" line as markdown, which is where a
	// plain listing loses its indent and its marker column. Assert on what the
	// user actually sees, not just on what was appended.
	rendered := renderMarkdownWith(m.markdownFor(70), msg.Content, 70)
	if !strings.Contains(rendered, "* model-a") {
		t.Fatalf("the current-model marker did not survive rendering: %q", rendered)
	}
	if !strings.Contains(rendered, "  model-b") {
		t.Fatalf("the listing lost its column alignment when rendered: %q", rendered)
	}
}

func TestDispatchBareModelOpensAsyncPickerWhileModelsStillLists(t *testing.T) {
	m := newModelSwitchTestModel(t, "model-a", "model-b")

	cmd := m.dispatchResolved("/model")
	if cmd == nil {
		t.Fatal("bare /model returned no asynchronous loading command")
	}
	if !m.modelPicker.active || !m.modelPicker.loading || m.modelPickerRequest != 1 {
		t.Fatalf("picker state after /model = %+v, request = %d", m.modelPicker, m.modelPickerRequest)
	}
	if len(m.messages) != 0 {
		t.Fatalf("bare /model blocked long enough to post transcript output: %+v", m.messages)
	}

	listed := newModelSwitchTestModel(t, "model-a", "model-b")
	if listCmd := listed.dispatchResolved("/models"); listCmd != nil {
		t.Fatal("/models must remain synchronous transcript output")
	}
	if msg := lastMessage(t, listed); !strings.Contains(msg.Content, "model-b") {
		t.Fatalf("/models posted %q, want the listing", msg.Content)
	}
}

func TestModelPickerLoadingSuccessSelectsCurrentModel(t *testing.T) {
	m := newModelSwitchTestModel(t, "model-a", "model-b", "model-c")
	m.session.SetModel("model-b")
	cmd := m.openModelPicker()

	msg, ok := cmd().(modelListMsg)
	if !ok {
		t.Fatalf("load command returned %T, want modelListMsg", cmd())
	}
	next, _ := m.Update(msg)
	m = next.(Model)
	if m.modelPicker.loading {
		t.Fatal("picker remained loading after the endpoint response")
	}
	if got, ok := m.modelPicker.choice(); !ok || got != "model-b" {
		t.Fatalf("initial choice = %q, %v; want current model-b", got, ok)
	}
}

func TestModelPickerLoadingFailureClosesAndReportsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"nope"}}`, http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	m := newModelSwitchTestModel(t, "model-a")
	m.cfg.BaseURL = srv.URL + "/v1"
	s, err := chat.NewSession(context.Background(), m.cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	m.session = s

	cmd := m.openModelPicker()
	next, _ := m.Update(cmd())
	m = next.(Model)
	if m.modelPicker.active {
		t.Fatal("picker remained active after endpoint failure")
	}
	if msg := lastMessage(t, m); msg.Role != "error" || !strings.Contains(msg.Content, "500") {
		t.Fatalf("endpoint failure = %+v, want error containing 500", msg)
	}
}

func TestModelPickerEscCancelsAndLateResponsesAreIgnored(t *testing.T) {
	m := newModelSwitchTestModel(t, "model-a", "model-b")
	oldCmd := m.openModelPicker()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.modelPicker.active || m.quitting {
		t.Fatalf("Esc picker state = %+v, quitting = %v", m.modelPicker, m.quitting)
	}

	newCmd := m.openModelPicker()
	next, _ = m.Update(oldCmd())
	m = next.(Model)
	if !m.modelPicker.active || !m.modelPicker.loading || len(m.modelPicker.all) != 0 {
		t.Fatalf("late response replaced active request: %+v", m.modelPicker)
	}

	next, _ = m.Update(newCmd())
	m = next.(Model)
	if m.modelPicker.loading || len(m.modelPicker.all) != 2 {
		t.Fatalf("current response was not accepted: %+v", m.modelPicker)
	}
}

func TestModelPickerKeysTakePriorityAndEditRuneSafely(t *testing.T) {
	m := newModelSwitchTestModel(t, "café", "cafeteria", "tea")
	m.modelPicker.open(1)
	m.modelPicker.setModels([]string{"café", "cafeteria", "tea"}, "café")
	m.awaitingApproval = true
	m.queue = []string{"queued-a", "queued-b"}
	m.queueSel = 1
	m.textarea.SetValue("composer draft")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f', '\n', 'é'}})
	m = next.(Model)
	if m.modelPicker.query != "fé" {
		t.Fatalf("picker query = %q, want only printable runes", m.modelPicker.query)
	}
	if m.textarea.Value() != "composer draft" || m.queueSel != 1 || !m.awaitingApproval {
		t.Fatalf("picker key leaked to lower-priority state: textarea=%q queueSel=%d approval=%v", m.textarea.Value(), m.queueSel, m.awaitingApproval)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = next.(Model)
	if m.modelPicker.query != "f" {
		t.Fatalf("query after Backspace = %q, want f", m.modelPicker.query)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = next.(Model)
	if m.modelPicker.query != "f " {
		t.Fatalf("query after printable Space = %q, want %q", m.modelPicker.query, "f ")
	}
}

func TestModelPickerNavigationAndEnterSwitch(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "model-a"}, {"id": "model-b"}}})
	}))
	t.Cleanup(srv.Close)
	cfg := types.Config{
		Model:   "model-a",
		BaseURL: srv.URL + "/v1",
		// A temp base dir keeps the developer's own provider.json (a saved
		// /login pick) from switching this session off the fake endpoint.
		BaseDir:    t.TempDir(),
		Compaction: types.CompactionConfig{MaxContextTokens: 128000},
	}
	s, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	m := newQueueTestModel()
	m.ctx, m.cfg, m.session = context.Background(), cfg, s
	cmd := m.openModelPicker()
	next, _ := m.Update(cmd())
	m = next.(Model)

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	if choice, _ := m.modelPicker.choice(); choice != "model-b" {
		t.Fatalf("Down selected %q, want model-b", choice)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(Model)
	if choice, _ := m.modelPicker.choice(); choice != "model-a" {
		t.Fatalf("Up selected %q, want model-a", choice)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.modelPicker.active || m.session.Model() != "model-b" {
		t.Fatalf("Enter picker=%+v model=%q, want closed on model-b", m.modelPicker, m.session.Model())
	}
	// A /model pick is not saved, and the confirmation says so.
	wantSwitch := "model: model-b · " + theme.ModelSessionOnly
	if msg := lastMessage(t, m); msg.Role != "agent" || msg.Content != wantSwitch {
		t.Fatalf("switch confirmation = %+v, want %q", msg, wantSwitch)
	}
	if requests != 1 {
		t.Fatalf("model endpoint requests = %d, want one load and no switch validation", requests)
	}
}

func TestModelPickerEnterWithoutMatchIsNoOp(t *testing.T) {
	m := newModelSwitchTestModel(t, "model-a", "model-b")
	m.modelPicker.open(1)
	m.modelPicker.setModels([]string{"model-a", "model-b"}, "model-a")
	m.modelPicker.appendQuery("missing")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd != nil || !m.modelPicker.active || m.session.Model() != "model-a" || len(m.messages) != 0 {
		t.Fatalf("no-match Enter changed state: cmd=%v picker=%+v model=%q messages=%+v", cmd != nil, m.modelPicker, m.session.Model(), m.messages)
	}
}

func TestViewRendersPickerDialogNotComposer(t *testing.T) {
	m := newModelSwitchTestModel(t, "model-a", "model-b")
	m.width, m.height = 80, 12
	m.textarea.SetValue("composer-secret")
	m.modelPicker.open(1)
	m.updateDimensions()
	m.updateViewport()

	view := m.View()
	if !strings.Contains(view, "search:") || !strings.Contains(view, "loading models") {
		t.Fatalf("view does not contain picker dialog: %q", view)
	}
	if strings.Contains(view, "composer-secret") {
		t.Fatalf("view exposed the composer while picker active: %q", view)
	}
}

func TestModelPickerResizeKeepsSelectionVisibleWithinFrame(t *testing.T) {
	models := modelPickerFrameItems(20)
	m := newModelSwitchTestModel(t, models...)
	m.width, m.height, m.maxHeight = 80, 24, 12
	m.modelPicker.open(1)
	m.modelPicker.setModels(models, "model-19")
	m.updateDimensions()

	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = next.(Model)
	visible := modelPickerMaxVisible
	if m.modelPicker.selected < m.modelPicker.offset || m.modelPicker.selected >= m.modelPicker.offset+visible {
		t.Fatalf("selected=%d offset=%d visible=%d: selection is outside the resized window", m.modelPicker.selected, m.modelPicker.offset, visible)
	}
	if got := lipgloss.Height(m.View()); got > m.effectiveHeight() {
		vs := m.viewState()
		footer, footerHeight := m.renderFooter(vs, m.width)
		t.Fatalf("frame height=%d effective=%d viewport=%d composer=%d header=%d footer=%d/%d chrome=%d visible=%d",
			got, m.effectiveHeight(), m.viewport.Height, lipgloss.Height(m.renderComposer(m.width)),
			m.presenter.HeaderHeight(vs), lipgloss.Height(footer), footerHeight, m.chromeBudget, visible)
	}
}

func TestDispatchModelSetSwitchesTheSession(t *testing.T) {
	m := newModelSwitchTestModel(t, "model-a", "model-b")

	if cmd := m.dispatchResolved("/model model-b"); cmd != nil {
		t.Fatal("/model <name> must not start a turn")
	}
	msg := lastMessage(t, m)
	if msg.Role != "agent" || !strings.Contains(msg.Content, "model-b") {
		t.Fatalf("confirmation = %+v, want an agent line naming model-b", msg)
	}
	if got := m.session.Model(); got != "model-b" {
		t.Fatalf("session model = %q, want model-b", got)
	}
}

// A typo must be refused in the transcript, as an error, with the session left
// where it was: SetModel's own error only covers a rebuild failure, not an
// unserved model name, so this is the only place the mistake can still be
// caught before the next turn 404s.
//
// The refusal arrives as TWO lines. The listing rides an "agent" line inside a
// fence because the "error" role is word-wrapped, and a wrapped listing loses
// the indent column and clips long model IDs, which is the same failure the
// plain markdown path had. The names here are deliberately long and the render
// width deliberately narrow, so a regression shows up.
func TestDispatchModelSetRejectsAnUnknownName(t *testing.T) {
	const long = "hf.co/unsloth/gemma-4-27b-GGUF:Q4_K_M"
	m := newModelSwitchTestModel(t, "qwen3-coder-30b", long)

	if cmd := m.dispatchResolved("/model qwen3-codr-30b"); cmd != nil {
		t.Fatal("a refused /model must not start a turn")
	}
	if len(m.messages) < 2 {
		t.Fatalf("want a headline and a listing, got %d message(s): %+v", len(m.messages), m.messages)
	}
	headline, listing := m.messages[len(m.messages)-2], m.messages[len(m.messages)-1]

	if headline.Role != "error" {
		t.Fatalf("headline posted as %q, want error", headline.Role)
	}
	if !strings.Contains(headline.Content, "qwen3-codr-30b") {
		t.Fatalf("headline = %q, want it to name the typo", headline.Content)
	}
	if strings.Contains(headline.Content, long) {
		t.Fatalf("the listing must not ride on the wrapped error line: %q", headline.Content)
	}
	if listing.Role != "agent" {
		t.Fatalf("listing posted as %q, want agent", listing.Role)
	}

	// 30 columns: narrow enough that the long ID cannot fit, which is where a
	// re-wrapped listing loses its indent and its marker column.
	rendered := renderMarkdownWith(m.markdownFor(30), listing.Content, 30)
	if !strings.Contains(rendered, "* qwen3-coder-30b") {
		t.Fatalf("the current-model marker did not survive a narrow render: %q", rendered)
	}
	if !strings.Contains(rendered, "  hf.co/unsloth/gemma-4-27b-") {
		t.Fatalf("the alternative lost its indent at a narrow width: %q", rendered)
	}
	if got := m.session.Model(); got != "qwen3-coder-30b" {
		t.Fatalf("session model = %q, want the switch refused", got)
	}
}

func TestModelPickerDialogStates(t *testing.T) {
	t.Run("loading", func(t *testing.T) {
		m := newModelSwitchTestModel(t, "model-a")
		m.modelPicker.open(1)
		d := m.buildModelPickerDialog()
		if d.Hint != theme.ModelPickerLoading {
			t.Fatalf("hint = %q, want %q", d.Hint, theme.ModelPickerLoading)
		}
		if len(d.Options) != 0 {
			t.Fatalf("loading dialog should have no options, got %d", len(d.Options))
		}
	})
	t.Run("empty endpoint", func(t *testing.T) {
		m := newModelSwitchTestModel(t, "model-a")
		m.modelPicker.open(1)
		m.modelPicker.setModels([]string{}, "model-a")
		d := m.buildModelPickerDialog()
		if d.Hint != theme.ModelPickerEmpty {
			t.Fatalf("hint = %q, want %q", d.Hint, theme.ModelPickerEmpty)
		}
	})
	t.Run("no search matches", func(t *testing.T) {
		m := newModelSwitchTestModel(t, "model-a", "model-b")
		m.modelPicker.open(1)
		m.modelPicker.setModels([]string{"model-a", "model-b"}, "model-a")
		m.modelPicker.appendQuery("xyz")
		d := m.buildModelPickerDialog()
		if d.Hint != theme.ModelPickerNoMatches {
			t.Fatalf("hint = %q, want %q", d.Hint, theme.ModelPickerNoMatches)
		}
	})
	t.Run("matches with current marker and key hint", func(t *testing.T) {
		m := newModelSwitchTestModel(t, "model-a", "model-b", "model-c")
		m.modelPicker.open(1)
		m.modelPicker.setModels([]string{"model-a", "model-b", "model-c"}, "model-b")
		d := m.buildModelPickerDialog()
		if d.Kind != render.DialogModelPicker {
			t.Fatalf("kind = %v, want DialogModelPicker", d.Kind)
		}
		// /model's picker also points at /login, the only way to switch provider.
		if want := theme.ModelPickerSessionKeyHint + " · " + theme.ModelPickerLoginHint; d.Hint != want {
			t.Fatalf("hint = %q, want %q", d.Hint, want)
		}
		if !strings.Contains(d.Title, theme.ModelPickerSearchLabel) {
			t.Fatalf("title = %q, want search label", d.Title)
		}
		// model-a is the session's current model, so it carries the marker.
		found := false
		for _, opt := range d.Options {
			if strings.Contains(opt.Text, "model-a") && strings.Contains(opt.Text, "(current)") {
				found = true
			}
		}
		if !found {
			t.Fatalf("no option marks model-a as current: %+v", d.Options)
		}
		// setModels(..., "model-b") placed the cursor on model-b (index 1).
		if d.Selected != 1 {
			t.Fatalf("selected = %d, want 1 (model-b)", d.Selected)
		}
	})
}
