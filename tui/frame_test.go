package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/tui/render"
	"github.com/mudler/nib/tui/render/full"
	"github.com/mudler/nib/tui/render/inline"
)

// frameModel builds a Model sized like a real 80x24 terminal, with the
// components View and updateViewport touch.
func frameModel() Model {
	m := newTestModel(Model{
		viewport: viewport.New(80, 10),
		textarea: textarea.New(),
		width:    80,
		height:   24,
	})
	m.updateDimensions()
	return m
}

// TestViewStateIsComplete pins the ViewState contract: viewState() alone
// produces the whole per-frame projection. It used to stop at the transcript
// and let View fill in Help/Badges/Err/Footers/NewOutput by MUTATING the struct
// between presenter calls, so Header and Footer saw different states and a
// full-frame presenter composing from one ViewState got zero-valued footers.
func TestViewStateIsComplete(t *testing.T) {
	m := frameModel()
	m = withMessages(m, ChatMessage{Role: "user", Content: "hello"})
	m.jobs = []agentJob{{ID: "a1", Type: "explore", Status: chat.AgentStatusRunning}}
	m.err = errFrameTest

	vs := m.viewState()

	if vs.Help != "" {
		t.Error("ordinary viewState populated contextual Help")
	}
	if vs.Err != errFrameTest.Error() {
		t.Errorf("viewState Err = %q, want %q", vs.Err, errFrameTest.Error())
	}
	var agent bool
	for _, f := range vs.Footers {
		agent = agent || (f.Kind == render.FooterJobs && strings.HasPrefix(f.Text, "explore"))
	}
	if !agent {
		t.Errorf("viewState Footers = %+v, want a chip for the running explore agent", vs.Footers)
	}
	if vs.Brand == "" || vs.Summary.Primary == "" {
		t.Errorf("viewState left Brand/Summary empty: %q / %q", vs.Brand, vs.Summary.Primary)
	}
}

// TestViewDoesNotMutateViewState is the other half of the same contract: View
// must render Header and Footer from one value, so whatever Footer sees was
// already there when Header was called.
func TestViewDoesNotMutateViewState(t *testing.T) {
	m := frameModel()
	m = withMessages(m, ChatMessage{Role: "user", Content: "hello"})
	m.jobs = []agentJob{{ID: "a1", Type: "explore", Status: chat.AgentStatusRunning}}

	before := m.viewState()
	_ = m.View()
	after := m.viewState()

	if before.Help != after.Help || len(before.Footers) != len(after.Footers) || before.Err != after.Err {
		t.Errorf("View left the frame projection different: before %+v, after %+v", before, after)
	}
	// The footer rows must reach the rendered frame from the same projection
	// Header rendered from — i.e. without View filling them in afterwards.
	out := m.View()
	if !strings.Contains(out, "explore") {
		t.Errorf("rendered frame lost the agent chip: %q", out)
	}
}

// TestFrameNeverPairsWorkingWithHistoricalToolAge locks the original UX bug:
// a completed root-tool receipt must not read like a tool that is still
// running. The pinned row reports the authoritative current phase, while the
// receipt remains available only in technical details.
func TestFrameNeverPairsWorkingWithHistoricalToolAge(t *testing.T) {
	m := frameModel()
	m.loading = true
	m.toolEvents = newToolEventQueue()
	m.toolEvents.begin()
	started := time.Unix(100, 0)
	m.syncActivityPhase(started)
	seedSummaryStatus(&m)
	m.toolEvents.observationCallback()(chat.Observation{
		Kind:       "tool started",
		OwnerKnown: true,
		Received:   started.Add(-10 * time.Minute),
		Order:      1,
	})

	for _, p := range []render.Presenter{full.New(), inline.New()} {
		m.presenter = p
		out := m.presenter.Footer(m.viewStateAt(started.Add(12*time.Second)), m.width)
		if !strings.Contains(out, "Working") || !strings.Contains(out, "12s") {
			t.Fatalf("footer lost current phase: %q", out)
		}
		if strings.Contains(out, "tool started") || strings.Contains(out, "ago") {
			t.Fatalf("footer exposed historical receipt as current activity: %q", out)
		}
	}
}

// TestNewOutputResolvedInViewState: the scroll-position signal is a projection
// decision (is the viewport even on screen, and is it parked at the bottom),
// not something View computes on the side.
func TestNewOutputResolvedInViewState(t *testing.T) {
	m := frameModel()
	for i := 0; i < 60; i++ {
		m = withMessages(m, ChatMessage{Role: "user", Content: "history line"})
	}
	m.updateViewport()
	if vs := m.viewState(); vs.NewOutput {
		t.Error("NewOutput set while parked at the bottom")
	}
	m.viewport.SetYOffset(0)
	if vs := m.viewState(); !vs.NewOutput {
		t.Error("NewOutput not set while scrolled up with content below the fold")
	}
	// The log viewer owns the body: there is no transcript viewport to be
	// scrolled up in, so the marker must be off.
	m.showLogs = true
	if vs := m.viewState(); vs.NewOutput {
		t.Error("NewOutput set while the log viewer owns the body")
	}
	if len(m.viewState().Footers) != 0 {
		t.Error("footer rows rendered while the log viewer owns the body")
	}
}

// TestViewportBudgetsAgainstFooterHeight is the C1 regression: the layout used
// to reserve a fixed three rows for a footer that emits between one and seven,
// so a session with a running sub-agent and a live loop was two rows over
// budget. On the alt screen that makes bubbletea scroll the composed frame and
// the header walks off the top.
func TestViewportBudgetsAgainstFooterHeight(t *testing.T) {
	m := frameModel()
	bare := m.viewport.Height

	// Contextual help, expanded telemetry, and an error: three footer rows the
	// fixed budget never accounted for.
	m.sessionCreated = time.Now()
	m.activityFocus = true
	m.err = errFrameTest
	m.updateDimensions()

	if got := m.viewport.Height; got != bare-3 {
		t.Errorf("viewport height with three extra footer rows = %d, want %d", got, bare-3)
	}
}

// TestFrameFitsTheTerminal is the property the budget exists for: whatever the
// footer is doing, the composed frame must not be taller than the terminal.
//
// The presenter is part of each case, not a constant: every case here used to
// run on frameModel()'s default inline.New(), whose Frame never places
// v.Dialogs (the core bakes them into the viewport's scrollback instead), so a
// pending dialog cost inline nothing and this test could not see the rows
// full.Frame writes between body and composer. That is exactly how a
// ten-row approval card shipped as an eight-row overflow on the default
// surface.
func TestFrameFitsTheTerminal(t *testing.T) {
	cases := []struct {
		name      string
		presenter render.Presenter
		setup     func(m *Model)
	}{
		{"bare", inline.New(), func(m *Model) {}},
		{"one job", inline.New(), func(m *Model) {
			m.jobs = []agentJob{{ID: "a1", Type: "explore", Status: chat.AgentStatusRunning}}
		}},
		{"job, error and a scrolled-up viewport", inline.New(), func(m *Model) {
			m.jobs = []agentJob{{ID: "a1", Type: "explore", Status: chat.AgentStatusRunning}}
			m.err = errFrameTest
			m.viewport.SetYOffset(0)
		}},
		{"full surface, pending approval", full.New(), func(m *Model) {
			m.awaitingApproval = true
			m.pendingTool = &chat.ToolCallRequest{
				Name:      "bash",
				Arguments: `{"command":"ls -la"}`,
				Reasoning: "listing the directory before editing anything in it",
			}
		}},
		{"full surface, resume picker", full.New(), func(m *Model) {
			m.awaitingResume = true
			m.resumeList = &render.SelectList{
				Items:      resumeItems(fakeSessions(12)),
				MaxVisible: 8,
			}
		}},
		{"inline surface, model picker", inline.New(), func(m *Model) {
			m.modelPicker.open(1)
			m.modelPicker.setModels(modelPickerFrameItems(20), "model-19")
		}},
		{"full surface, model picker", full.New(), func(m *Model) {
			m.modelPicker.open(1)
			m.modelPicker.setModels(modelPickerFrameItems(20), "model-19")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := frameModel()
			m.presenter = tc.presenter
			for i := 0; i < 60; i++ {
				m = withMessages(m, ChatMessage{Role: "user", Content: "history line"})
			}
			tc.setup(&m)
			m.updateDimensions()
			m.updateViewport()

			if got := lipgloss.Height(m.View()); got > m.height {
				t.Errorf("frame is %d rows tall, terminal is %d: the header scrolls off the top", got, m.height)
			}
		})
	}
}

// TestFrameFillsTerminalOnFullSurface is the underflow counterpart of
// TestFrameFitsTheTerminal: a full-surface (alt-screen) presenter owns the
// whole terminal, so the composed frame must fill every row, not just avoid
// exceeding the height. The viewport self-pads to its allotted height, but the
// first-run empty state and the log-viewer job list do not, so without
// height-fill padding in View the footer floats above the bottom.
func TestFrameFillsTerminalOnFullSurface(t *testing.T) {
	cases := []struct {
		name  string
		setup func(m *Model)
	}{
		{"empty state", func(m *Model) {}},
		{"log viewer", func(m *Model) {
			m.showLogs = true
		}},
		{"viewport filled", func(m *Model) {
			for i := 0; i < 60; i++ {
				m.appendMessage(ChatMessage{Role: "user", Content: "history line"})
			}
			m.updateViewport()
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := frameModel()
			m.presenter = full.New()
			tc.setup(&m)
			m.updateDimensions()

			got := lipgloss.Height(m.View())
			if got != m.height {
				t.Errorf("full-surface frame is %d rows, terminal is %d: footer does not snap to the bottom", got, m.height)
			}
		})
	}
}

func modelPickerFrameItems(n int) []string {
	items := make([]string, n)
	for i := range items {
		items[i] = "model-" + strconv.Itoa(i)
	}
	return items
}

// fakeSessions builds n stored session records for the /resume picker.
func fakeSessions(n int) []chat.SessionRecord {
	out := make([]chat.SessionRecord, n)
	for i := range out {
		out[i] = chat.SessionRecord{
			ID:      "s" + strconv.Itoa(i),
			Title:   "session number " + strconv.Itoa(i),
			Updated: time.Now().Add(-time.Duration(i) * time.Hour),
		}
	}
	return out
}

// TestFooterBudgetRecomputesMidTurn: a job starting is not a WindowSizeMsg, but
// it changes the footer's height all the same. The budget has to follow it,
// otherwise the frame only becomes correct again the next time the terminal is
// resized.
func TestFooterBudgetRecomputesMidTurn(t *testing.T) {
	m := frameModel()
	for i := 0; i < 60; i++ {
		m = withMessages(m, ChatMessage{Role: "user", Content: "history line"})
	}
	m.updateViewport()
	bare := m.viewport.Height

	bareFrame := lipgloss.Height(m.View())

	// Mid-turn: an error line appears. No resize, just a re-render.
	m.err = errFrameTest
	m.updateViewport()
	// The user-visible symptom: before the fix the frame grew by the job row
	// instead of the viewport giving the row up, so on the alt screen
	// bubbletea scrolled the frame and the header walked off the top.
	if got := lipgloss.Height(m.View()); got != bareFrame {
		t.Errorf("frame height changed from %d to %d when an error row appeared", bareFrame, got)
	}
	if got := m.viewport.Height; got != bare-1 {
		t.Errorf("viewport height after an error row appeared = %d, want %d", got, bare-1)
	}
	if got := lipgloss.Height(m.View()); got > m.height {
		t.Errorf("frame is %d rows tall, terminal is %d", got, m.height)
	}

	// And back: the row goes away, the row comes back to the viewport.
	m.err = nil
	m.updateViewport()
	if got := m.viewport.Height; got != bare {
		t.Errorf("viewport height after the error row went away = %d, want %d", got, bare)
	}
}

// recordingPresenter wraps the inline presenter and records what the model
// asked it for, so a test can prove the model ASKS rather than reconstructing
// one surface's chrome for both.
type recordingPresenter struct {
	render.Presenter
	width      int
	askedRoles []render.Role
}

func (p *recordingPresenter) ContentWidth(role render.Role, w int) int {
	p.askedRoles = append(p.askedRoles, role)
	return p.width
}

// TestMarkdownWidthComesFromThePresenter is the I2 regression: the model used
// to rebuild the inline widget's `nib · ` label itself to work out the glamour
// wrap width, and applied that to BOTH surfaces. The moment `full` gets its own
// gutter, assistant markdown would wrap to the wrong column with no test
// failing. It must ask the presenter instead.
func TestMarkdownWidthComesFromThePresenter(t *testing.T) {
	const stubWidth = 31
	rec := &recordingPresenter{Presenter: inline.New(), width: stubWidth}
	m := newTestModel(Model{
		viewport:  viewport.New(80, 10),
		textarea:  textarea.New(),
		width:     80,
		height:    24,
		presenter: rec,
	})
	m = withMessages(m,
		ChatMessage{Role: "assistant", Content: "an answer"},
		ChatMessage{Role: "agent", AgentID: "a1", Content: "a sub-agent line"},
	)
	m.updateViewport()

	var sawAssistant, sawAgent bool
	for _, role := range rec.askedRoles {
		switch role {
		case render.RoleAssistant:
			sawAssistant = true
		case render.RoleAgent:
			sawAgent = true
		}
	}
	if !sawAssistant || !sawAgent {
		t.Errorf("model did not ask the presenter for content widths: asked %v", rec.askedRoles)
	}
	if _, ok := m.mdRenderers[stubWidth]; !ok {
		t.Errorf("markdown was not pre-rendered at the presenter's width %d; renderers built for %v",
			stubWidth, rendererWidths(m))
	}
}

func rendererWidths(m Model) []int {
	var out []int
	for w := range m.mdRenderers {
		out = append(out, w)
	}
	return out
}

// TestToolLabelFormattedModelSide is the I5 regression: the presenters used to
// import chat and format the tool label themselves, which was the only reason
// render.Message carried Name and Arguments. The label now arrives formatted.
func TestToolLabelFormattedModelSide(t *testing.T) {
	want := toolLabel("bash", `{"command":"ls -la"}`)
	if want == "bash" {
		t.Fatalf("precondition: toolLabel should summarise the call, got %q", want)
	}

	m := frameModel()
	m = withMessages(m, ChatMessage{Role: "tool", Name: "bash", Arguments: `{"command":"ls -la"}`, Content: "output"})
	m.updateViewport()
	if out := m.viewport.View(); !strings.Contains(out, want) {
		t.Errorf("rendered tool block lost the formatted label %q: %q", want, out)
	}
}

// errFrameTest is a fixed error for the footer's error line.
var errFrameTest = frameTestError("something failed")

type frameTestError string

func (e frameTestError) Error() string { return string(e) }

// TestTypingDoesNotOverflowTheFrame is the flicker regression. syncLayout ran
// only from updateViewport, and nothing on the keystroke path calls it — so a
// composer that changed height under the user's own typing (the `/` completion
// popup opening, then narrowing, then closing) left the viewport budgeted for
// the old chrome. The composed frame was up to a dozen rows TALLER than the
// terminal while the popup was open and, once a tick had re-budgeted it,
// rows SHORTER the moment the popup closed. Bubble Tea's inline renderer drops
// lines from the top of an over-tall frame and erases the screen below a short
// one, so the transcript jumped away and snapped back on the next 1s
// shellTick: the flicker the user reported while typing.
//
// The invariant: every keystroke leaves the frame exactly as tall as the one
// updateViewport budgeted.
func TestTypingDoesNotOverflowTheFrame(t *testing.T) {
	m := frameModel()
	m.sessionReady = true
	m.textarea.SetHeight(1)
	m.textarea.Focus()
	m.completion.setRegistries(nil, nil, nil)
	for i := 0; i < 60; i++ {
		m = withMessages(m, ChatMessage{Role: "user", Content: "history line"})
	}
	m.updateDimensions()
	m.updateViewport()
	want := lipgloss.Height(m.View())
	if want != m.height {
		t.Fatalf("baseline frame is %d rows, terminal is %d", want, m.height)
	}

	type step struct {
		label string
		msg   tea.Msg
	}
	steps := []step{
		{"/", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}}},
		{"m", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}}},
		{"o", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}}},
		// Navigating the popup can add or drop its ghost-hint row.
		{"down", tea.KeyMsg{Type: tea.KeyDown}},
		{"up", tea.KeyMsg{Type: tea.KeyUp}},
		// Tab accepts, which narrows the popup to the accepted verb.
		{"tab", tea.KeyMsg{Type: tea.KeyTab}},
		// A space ends the verb, so the popup closes: the rows it was using
		// must come back to the viewport in the same frame.
		{"space", tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}},
	}
	var cur tea.Model = m
	for _, s := range steps {
		next, _ := cur.Update(s.msg)
		cur = next
		mm := cur.(Model)
		if got := lipgloss.Height(mm.View()); got != want {
			t.Errorf("after typing %q the frame is %d rows tall, want %d (composer %d rows, viewport %d)",
				s.label, got, want, lipgloss.Height(mm.renderComposer(mm.width)), mm.viewport.Height)
		}
	}
}
