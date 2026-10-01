package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"

	"github.com/mudler/nib/theme"
)

// Generic loading is factual footer state, not an animated cognition label.
func TestLoadingSummaryReplacesGenericSpinner(t *testing.T) {
	s := spinner.New()
	s.Spinner = spinner.Spinner{Frames: theme.SpinnerFrames(), FPS: spinnerFPS}

	m := newTestModel(Model{
		viewport: viewport.New(80, 10),
		width:    80,
		spinner:  s,
		loading:  true,
	})
	m.updateViewport()

	out := m.viewport.View()
	frame := theme.SpinnerFrames()[0]
	if strings.Contains(out, frame) {
		t.Errorf("loading block retained generic spinner frame %q; got:\n%s", frame, out)
	}
	if strings.Contains(out, theme.VerbThinking) {
		t.Errorf("loading block retained status verb %q; got:\n%s", theme.VerbThinking, out)
	}
	if !strings.Contains(m.View(), "Working") {
		t.Fatal("missing pinned working state")
	}
}
