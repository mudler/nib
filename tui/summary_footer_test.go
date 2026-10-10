package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
	"github.com/mudler/nib/tui/render/full"
	"github.com/mudler/nib/tui/render/inline"
)

func TestSummaryFooterCacheInputs(t *testing.T) {
	v := render.ViewState{Summary: render.ActivitySummary{Primary: "Ready"}, Footers: []render.FooterRow{{Text: "jobs"}}}
	for _, change := range []func(*render.ViewState){
		func(v *render.ViewState) { v.Summary.Primary = "Waiting for jobs" },
		func(v *render.ViewState) { v.Summary.Agents = 123 },
		func(v *render.ViewState) { v.Summary.Shells = 12 },
		func(v *render.ViewState) { v.Summary.CountsKnown = true },
		func(v *render.ViewState) { v.Summary.Schedule = "next run in 2m" },
		func(v *render.ViewState) { v.Summary.Updating = true },
		func(v *render.ViewState) { v.Summary.Compact = "Wait" },
		func(v *render.ViewState) { v.Summary.Secondary = "received 2s ago" },
		func(v *render.ViewState) { v.Summary.Counts = "shell 1" },
		func(v *render.ViewState) { v.Summary.Marker = render.SummaryMarkerWorking },
		func(v *render.ViewState) { v.Expanded = "more telemetry" },
		func(v *render.ViewState) { v.HelpRight = "hint" },
		func(v *render.ViewState) { v.Footers[0].Selected = true },
		func(v *render.ViewState) { v.Footers[0].Alert = "failed" },
		func(v *render.ViewState) { v.Footers[0].State = render.ChipActive },
	} {
		before := footerCacheKey(v, 80)
		next := v
		next.Footers = append([]render.FooterRow(nil), v.Footers...)
		change(&next)
		if before == footerCacheKey(next, 80) {
			t.Fatalf("cache ignored changed footer: %+v", next)
		}
	}
}

func TestSummaryFooterCacheTracksActiveSpinnerOnly(t *testing.T) {
	m := frameModel()
	m.footerCache = &footerCache{}
	v := render.ViewState{Summary: render.ActivitySummary{Primary: "Working", Marker: render.SummaryMarkerWorking}, Spinner: theme.SpinnerFrames()[0]}
	a, _ := m.renderFooter(v, 80)
	v.Spinner = theme.SpinnerFrames()[1]
	b, _ := m.renderFooter(v, 80)
	if a == b {
		t.Fatalf("active cached footer did not animate: %q", a)
	}
	v.Summary = render.ActivitySummary{Primary: "Ready", Marker: render.SummaryMarkerReady}
	a, _ = m.renderFooter(v, 80)
	v.Spinner = theme.SpinnerFrames()[2]
	b, _ = m.renderFooter(v, 80)
	if a != b {
		t.Fatalf("stable cached footer changed with spinner: %q != %q", a, b)
	}
}
func TestSummaryFooterSilentIdleTick(t *testing.T) {
	for _, state := range []struct {
		name   string
		parked bool
	}{{name: "working"}, {name: "parked", parked: true}} {
		t.Run(state.name, func(t *testing.T) {
			m := frameModel()
			m.footerCache = &footerCache{}
			m.loading = !state.parked
			m.parked = state.parked
			m.toolEvents = newToolEventQueue()
			m.toolEvents.begin()
			now := time.Unix(100, 0)
			m.syncActivityPhase(now)
			seedSummaryStatus(&m)
			m.toolEvents.observationCallback()(chat.Observation{OwnerKnown: true, HasText: true, Received: now, Order: 1})
			before := m.toolEvents.rootObservation()
			first := m.viewStateAt(now.Add(time.Second))
			a, ah := m.renderFooter(first, 120)
			next, cmd := m.Update(hudTickMsg{})
			m = next.(Model)
			if cmd == nil {
				t.Fatal("existing idle tick stopped")
			}
			// This test isolates footer age/cache behavior from collection.
			seedSummaryStatus(&m)
			second := m.viewStateAt(now.Add(3 * time.Second))
			b, bh := m.renderFooter(second, 120)
			if a == b || ah != bh || first.Summary.Primary != second.Summary.Primary || !strings.Contains(b, "3s") || strings.Contains(b, "ago") {
				t.Fatalf("%q -> %q", a, b)
			}
			if before != m.toolEvents.rootObservation() {
				t.Fatal("tick fabricated an observation")
			}
			c, ch := m.renderFooter(second, 120)
			if c != b || ch != bh {
				t.Fatal("unchanged cache differs")
			}
		})
	}
}
func TestSummaryFooterLayoutAndApproval(t *testing.T) {
	for _, p := range []render.Presenter{inline.New(), full.New()} {
		for _, h := range []int{8, 24, 40} {
			m := frameModel()
			m.presenter = p
			m.height = h
			m.sessionReady = true
			m.awaitingApproval = true
			m.activityFocus = true
			m.err = errFrameTest
			m = withMessages(m, ChatMessage{Role: "user", Content: strings.Repeat("history\n", 100)})
			m.updateViewport()
			v := m.viewState()
			m.syncLayout(v)
			footer, fh := m.renderFooter(v, m.width)
			if fh != lipgloss.Height(footer) || m.footerBudget != fh {
				t.Fatal("footer height not budgeted")
			}
			if !strings.Contains(m.View(), "Approval needed") || m.loading {
				t.Fatal("idle approval missing")
			}
			if m.viewport.Height != max(1, m.effectiveHeight()-m.layoutBudget(v, fh)) {
				t.Fatal("summary did not reserve transcript space")
			}
			m.showLogs = true
			m.updateViewport()
			if !strings.Contains(m.View(), "Approval needed") {
				t.Fatal("details hide summary")
			}
		}
	}
}

func TestSummaryFooterCachedSelectionAndExpandedHeight(t *testing.T) {
	m := frameModel()
	m.footerCache = &footerCache{}
	v := m.viewStateAt(time.Unix(100, 0))
	v.Footers = []render.FooterRow{{Text: "shell 1", State: render.ChipActive}}
	a, ah := m.renderFooter(v, 80)
	v.Footers[0].Selected = true
	b, bh := m.renderFooter(v, 80)
	if a == b || ah != bh {
		t.Fatal("cached selection did not change chip alone")
	}
	v.Expanded = "expanded telemetry"
	c, ch := m.renderFooter(v, 80)
	if c == b || ch != bh+1 {
		t.Fatal("cached expanded telemetry did not reserve a row")
	}
	if c != m.presenter.Footer(v, 80) || ch != m.presenter.FooterHeight(v, 80) {
		t.Fatal("cached output differs from presenter")
	}
}

func TestCompactFooterPlacementAndFit(t *testing.T) {
	for _, p := range []render.Presenter{inline.New(), full.New()} {
		for _, phase := range []string{"ready", "working", "running"} {
			m := frameModel()
			m.presenter = p
			m.cfg.UI.FooterFront = "clock"
			m.hudClock = "12:34:56"
			m.loading = phase != "ready"
			if phase == "running" {
				m.startTool(chat.ToolStart{Name: "bash", Arguments: "{}"})
			}
			for _, hint := range []string{"", "\x1b[31mcontext 界 guidance with many keys\x1b[0m"} {
				m.hint = hint
				for w := 0; w <= 160; w++ {
					m.width = w
					v := m.viewStateAt(time.Unix(100, 0))
					out, height := m.renderFooter(v, w)
					if w == 0 {
						if out != "" || height != 0 {
							t.Fatalf("zero width: %q / %d", out, height)
						}
						continue
					}
					if hint == "" && v.Help != "" {
						t.Fatalf("ordinary legend remains: %q", v.Help)
					}
					lines := strings.Split(out, "\n")
					wantHeight := render.TerminalRows(out, w)
					if height != wantHeight {
						t.Fatalf("width %d: height %d want %d: %q", w, height, wantHeight, out)
					}
					for _, line := range lines {
						if lipgloss.Width(line) > w && strings.Contains(line, " ") {
							t.Fatalf("width %d overflow: %q", w, line)
						}
					}
					last := lines[len(lines)-1]
					if v.Badges != "" && (!strings.HasSuffix(last, v.Badges) || lipgloss.Width(last) != w) {
						t.Fatalf("telemetry not flush right with status: %q", out)
					}
					if w >= 80 && !strings.Contains(last, v.Summary.Primary) {
						t.Fatalf("missing status: %q", out)
					}
				}
			}
		}
	}
}

func TestCompactTelemetryStrictFit(t *testing.T) {
	m := frameModel()
	m.hudClock = "12:34:56"
	m.hudCPU, m.hudCPUOK = 7, true
	for w := -1; w <= 8; w++ {
		got := m.telemetryLine([]string{"clock", "cpu"}, w)
		if lipgloss.Width(got) > max(w, 0) {
			t.Fatalf("width %d forced telemetry: %q", w, got)
		}
		if strings.Contains(got, "12:") && !strings.Contains(got, "12:34:56") {
			t.Fatal("partial clock")
		}
	}
	if got := m.telemetryLine([]string{"clock", "cpu"}, 6); !strings.Contains(got, "cpu 7%") {
		t.Fatalf("later fitting item omitted: %q", got)
	}
}

// Ordered from highest to lowest priority, matching the established focus routing.
func TestCompactFooterContextsAndPrecedence(t *testing.T) {
	cases := []struct {
		name, want string
		set        func(*Model)
	}{
		{"transient", "temporary hint", func(m *Model) { m.hint = "temporary hint" }},
		{"todo", theme.ScrollKeys + " scroll · esc/ctrl+t close", func(m *Model) { m.showTodo = true }},
		{"info", "esc close", func(m *Model) { m.infoPanel = "info" }},
		{"activity", theme.SideKeys + theme.HelpActivity, func(m *Model) { m.activityFocus = true }},
		{"agent input", "enter send · " + theme.ScrollKeys + " scroll · esc back · ctrl+o close", func(m *Model) {
			m.showLogs = true
			m.logOpenKind = "agent"
			m.logOpenID = "a"
			m.jobs = []agentJob{{ID: "a", Status: chat.AgentStatusRunning}}
		}},
		{"individual log", theme.ScrollKeys + " scroll · esc back · ctrl+o close", func(m *Model) { m.showLogs = true; m.logOpenID = "a" }},
		{"logs", theme.ScrollKeys + " select · enter open · k kill · esc close", func(m *Model) { m.showLogs = true }},
		{"approval edit", theme.HelpApprovalEdit, func(m *Model) { m.awaitingApproval = true; m.approvalEditing = true }},
		{"approval", theme.HelpApproval, func(m *Model) { m.awaitingApproval = true }},
		{"ask", theme.HelpAsk, func(m *Model) { m.awaitingAsk = true }},
		{"resume", theme.HelpResume, func(m *Model) { m.awaitingResume = true }},
		{"typed model", theme.ModelPickerTypeName, func(m *Model) { m.modelPicker.active = true; m.modelPicker.typed = true }},
		{"model", theme.ModelPickerKeyHint, func(m *Model) { m.modelPicker.active = true }},
		{"logout", theme.ProviderPickerLogoutHint, func(m *Model) { m.providerPicker.active = true; m.providerPicker.mode = pickerLogout }},
		{"endpoint picker", theme.EndpointPickerKeyHint, func(m *Model) { m.providerPicker.active = true; m.providerPicker.mode = pickerEndpoint }},
		{"provider", theme.ProviderPickerKeyHint, func(m *Model) { m.providerPicker.active = true }},
		{"login", theme.LoginFormHint, func(m *Model) { m.loginForm.active = true }},
		{"endpoint form", theme.EndpointFormHint, func(m *Model) { m.endpointForm.active = true }},
		{"login wait", theme.LoginWaitHint, func(m *Model) { m.loginWait.active = true }},
		{"foreground", theme.HelpForegroundWork, func(m *Model) { m.loading = true; m.jobs = []agentJob{{ID: "a", Status: chat.AgentStatusRunning}} }},
		{"parked", "enter add a follow-up · ctrl+c interrupt · ctrl+o logs", func(m *Model) { m.parked = true }},
		{"queue", "↑↓ pick · ^e edit · ^x delete", func(m *Model) { m.queue = queuedTexts("queued") }},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := frameModel()
			tc.set(&m)
			if got := m.helpLine(); got != tc.want {
				t.Fatalf("hint %q want %q", got, tc.want)
			}
			for _, p := range []render.Presenter{inline.New(), full.New()} {
				v := render.ViewState{Help: theme.Help.Render(m.helpLine()), Summary: render.ActivitySummary{Primary: "Approval needed"}}
				lines := strings.Split(p.Footer(v, 240), "\n")
				if len(lines) != 2 || !strings.Contains(lines[0], tc.want) || !strings.Contains(lines[1], "Approval needed") {
					t.Fatalf("context placement: %q", lines)
				}
			}
			// Exercise each overlapping lower-priority context without changing its text.
			for j := i + 1; j < len(cases); j++ {
				n := frameModel()
				cases[j].set(&n)
				tc.set(&n)
				if got := n.helpLine(); got != tc.want {
					t.Fatalf("over %s: %q want %q", cases[j].name, got, tc.want)
				}
			}
		})
	}
}

func TestCompactFooterCacheLayoutTransitions(t *testing.T) {
	for _, p := range []render.Presenter{inline.New(), full.New()} {
		for _, h := range []int{8, 24, 40} {
			m := frameModel()
			m.presenter = p
			m.height = h
			m.footerCache = &footerCache{}
			m.cfg.UI.FooterFront = "clock"
			m.hudClock = "12:34:56"
			for _, w := range []int{120, 40, 8, 2, 1, 0, 80} {
				m.width = w
				m.updateDimensions() // Real resize path owns geometry changes.
				for _, hint := range []string{"", "first hint", "second hint", ""} {
					m.hint = hint
					v := m.viewStateAt(time.Unix(100, 0))
					m.syncLayout(v)
					out, fh := m.renderFooter(v, w)
					if out != p.Footer(v, w) || fh != p.FooterHeight(v, w) {
						t.Fatal("stale cached footer")
					}
					if m.viewport.Height != max(1, m.effectiveHeight()-m.layoutBudget(v, fh)) {
						t.Fatal("stale viewport budget")
					}
					again, ah := m.renderFooter(v, w)
					if again != out || ah != fh {
						t.Fatal("unstable cache")
					}
					if w > 0 {
						want := render.TerminalRows(out, w)
						if fh != want {
							t.Fatalf("height %d want %d", fh, want)
						}
					}
				}
			}
		}
	}
}

// Exercise the event boundary: manually syncing layout would hide early-return bugs.
func TestCompactFooterHintUpdateLayout(t *testing.T) {
	for _, presenter := range []struct {
		name string
		p    render.Presenter
	}{{"inline", inline.New()}, {"full", full.New()}} {
		for _, tc := range []struct {
			name      string
			introduce tea.Msg
		}{
			{"clipboard error", composerClipboardMsg{err: errors.New("clipboard unavailable")}},
			{"oversized paste", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.Repeat("x", draftMaxBytes+1)), Paste: true}},
		} {
			for _, clear := range []tea.KeyType{tea.KeyEsc, tea.KeyEnd, tea.KeyCtrlV} {
				t.Run(fmt.Sprintf("%s/%s/%s", presenter.name, tc.name, (tea.KeyMsg{Type: clear}).String()), func(t *testing.T) {
					m := frameModel()
					m.presenter = presenter.p
					m.footerCache = &footerCache{}
					for i := 0; i < 40; i++ {
						m.appendMessage(ChatMessage{Role: "user", Content: fmt.Sprintf("history line %d", i)})
					}
					next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
					m = next.(Model)
					baseHeight := m.viewport.Height
					check := func(wantHeight int) {
						t.Helper()
						vs := m.viewState()
						fh := presenter.p.FooterHeight(vs, m.width)
						// Inspect the cache before View can refresh it.
						if m.footerCache.height != fh {
							t.Errorf("cached footer height %d, actual %d", m.footerCache.height, fh)
						}
						if m.viewport.Height != wantHeight {
							t.Errorf("viewport height %d, want %d", m.viewport.Height, wantHeight)
						}
						if h := lipgloss.Height(m.View()); h != 24 {
							t.Errorf("frame height %d, want 24", h)
						}
						if !m.viewport.AtBottom() {
							t.Error("lost transcript bottom pin")
						}
					}
					check(baseHeight)
					next, _ = m.Update(tc.introduce)
					m = next.(Model)
					if m.hint == "" {
						t.Fatal("event did not introduce hint")
					}
					check(baseHeight - 1)
					// A second error changes text without changing the row budget.
					next, _ = m.Update(composerClipboardMsg{err: errors.New("clipboard still unavailable")})
					m = next.(Model)
					check(baseHeight - 1)
					next, _ = m.Update(tea.KeyMsg{Type: clear})
					m = next.(Model)
					if m.hint != "" {
						t.Fatal("key did not clear hint")
					}
					check(baseHeight)
				})
			}
		}
	}
}
