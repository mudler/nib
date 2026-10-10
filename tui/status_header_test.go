package tui

import (
	"github.com/charmbracelet/x/ansi"
	"github.com/mudler/nib/tui/render"
	"github.com/mudler/nib/tui/render/full"
	"github.com/mudler/nib/tui/render/inline"
	"strings"
	"testing"
)

func TestStatusHeaderAcrossPanels(t *testing.T) {
	for _, p := range []render.Presenter{full.New(), inline.New()} {
		for _, panel := range []string{"conversation", "todo", "logs", "agent", "shell", "goal", "loops"} {
			m := frameModel()
			m.presenter = p
			m.width = 120
			m.sessionReady = true
			switch panel {
			case "todo":
				m.showTodo = true
			case "logs":
				m.showLogs = true
			case "agent", "shell":
				m.showLogs, m.logOpenID, m.logOpenKind = true, "job-123", panel
			case "conversation":
			default:
				m.infoPanel = panel
			}
			seedSummaryStatus(&m)
			phase := m.viewState().Summary.Primary
			if got := ansi.Strip(m.View()); !strings.HasPrefix(got, phase) {
				t.Errorf("%T %s: header must start with %q, got %.120q", p, panel, phase, got)
			}
		}
	}
}

func TestStatusLayoutMeasuresPhysicalChrome(t *testing.T) {
	for _, p := range []render.Presenter{full.New(), inline.New()} {
		for _, w := range []int{1, 3, 8, 20, 80} {
			m := frameModel()
			m.presenter = p
			m.width = w
			m.sessionReady = true
			m.awaitingApproval = true
			v := m.viewState()
			v.Dialogs = []render.Dialog{{Kind: render.DialogApproval, Title: "approval", Options: []render.DialogOption{{Text: "[y] approve"}, {Text: "[n] deny"}}}}
			_, fh := m.renderFooter(v, w)
			want := render.TerminalRows(p.Header(v), w) + max(1, render.TerminalRows(m.renderComposer(w), w)) + fh
			if p.Caps().OverlayDialogs {
				for _, d := range v.Dialogs {
					want += render.TerminalRows(p.Dialog(d, w), w)
				}
			}
			if got := m.layoutBudget(v, fh); got != want {
				t.Errorf("%T width %d budget %d want physical %d", p, w, got, want)
			}
		}
	}
}
