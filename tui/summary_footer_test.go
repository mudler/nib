package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/tui/render"
	"github.com/mudler/nib/tui/render/full"
	"github.com/mudler/nib/tui/render/inline"
)

func TestSummaryFooterCacheInputs(t *testing.T) {
	v := render.ViewState{Summary: render.ActivitySummary{Primary: "Ready"}, Footers: []render.FooterRow{{Text: "jobs"}}}
	for _, change := range []func(*render.ViewState){
		func(v *render.ViewState) { v.Summary.Primary = "Parked" },
		func(v *render.ViewState) { v.Summary.Compact = "Wait" },
		func(v *render.ViewState) { v.Summary.Secondary = "received 2s ago" },
		func(v *render.ViewState) { v.Summary.Counts = "shell 1" },
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
			m.toolEvents.observationCallback()(chat.Observation{OwnerKnown: true, HasText: true, Received: now, Order: 1})
			before := m.toolEvents.rootObservation()
			first := m.viewStateAt(now.Add(time.Second))
			a, ah := m.renderFooter(first, 120)
			next, cmd := m.Update(hudTickMsg{})
			m = next.(Model)
			if cmd == nil {
				t.Fatal("existing idle tick stopped")
			}
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
