package render_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/mudler/nib/tui/render"
)

// The real standard renderer receives WindowSizeMsg before the only nonempty
// frame. Quit flushes it synchronously; no sleeps or renderer mocks are needed.
type statusTransportModel struct {
	p     render.Presenter
	v     render.ViewState
	sized bool
}

func (m statusTransportModel) Init() tea.Cmd {
	return func() tea.Msg { return tea.WindowSizeMsg{Width: m.v.Width, Height: 1000} }
}
func (m statusTransportModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		m.sized = true
		return m, tea.Quit
	}
	return m, nil
}
func (m statusTransportModel) View() string {
	if !m.sized {
		return ""
	}
	return m.p.Frame(m.v, m.p.Header(m.v), "", "", m.p.Footer(m.v, m.v.Width), m.v.Width, 1000)
}
func TestStatusBubbleTeaTransport(t *testing.T) {
	for name, p := range presenters() {
		for _, w := range []int{1, 2, 3} {
			for _, phase := range []string{"Ready for input", "Working", "\x1b[31m界界 e\u0301clair\x1b[0m"} {
				t.Run(fmt.Sprintf("%s/%d/%s", name, w, ansi.Strip(phase)), func(t *testing.T) {
					v := render.ViewState{Width: w, Summary: render.ActivitySummary{Primary: phase}, Help: "ctrl+c interrupt"}
					var output bytes.Buffer
					program := tea.NewProgram(statusTransportModel{p: p, v: v}, tea.WithInput(nil), tea.WithOutput(&output), tea.WithoutSignalHandler(), tea.WithoutCatchPanics())
					if _, err := program.Run(); err != nil {
						t.Fatal(err)
					}
					emitted := strings.Trim(strings.ReplaceAll(ansi.Strip(output.String()), "\r", ""), "\n")
					joined := strings.Join(strings.Fields(emitted), "")
					wantPhase := strings.Join(strings.Fields(ansi.Strip(phase)), "")
					// A two-cell grapheme cannot exist in a one-cell terminal. It is
					// represented by one ASCII '?' rather than silently lost or split.
					if w == 1 {
						wantPhase = strings.ReplaceAll(wantPhase, "界", "?")
					}
					if !strings.HasPrefix(joined, wantPhase) || !strings.Contains(joined, "ctrl+cinterrupt") || !strings.HasSuffix(joined, wantPhase) {
						t.Errorf("renderer lost phase/control characters: emitted %q; want phase %q and ctrl+cinterrupt", emitted, wantPhase)
					}
					rows := len(strings.Split(emitted, "\n"))
					budget := p.HeaderHeight(v) + 2 + p.FooterHeight(v, w)
					if rows != budget {
						t.Errorf("renderer emitted %d rows, layout reserved %d: %q", rows, budget, emitted)
					}
					for _, line := range strings.Split(emitted, "\n") {
						if ansi.StringWidth(line) > w {
							t.Errorf("over-width output %q", line)
						}
					}
					t.Logf("captured width=%d rows=%d budget=%d: %q", w, rows, budget, emitted)
				})
			}
		}
	}
}
