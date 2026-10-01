package tui

import (
	"context"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
	"github.com/mudler/nib/tui/render/full"
	"github.com/mudler/nib/tui/render/inline"
)

func TestReasoningMotion(t *testing.T) {
	for name, p := range map[string]render.Presenter{"full": full.New(), "inline": inline.New()} {
		t.Run(name, func(t *testing.T) {
			m := streamModel()
			m.presenter = p
			text := strings.Repeat("日本語é", 16)
			next, _ := m.Update(delta(text))
			m = next.(Model)
			if got := m.viewState().Reasoning.Text; got != "" {
				t.Fatalf("reasoning burst visible before tick: %q", got)
			}
			if !m.animating() {
				t.Fatal("reasoning did not wake animation")
			}
			// Seed an independent assistant tail without folding: both lanes must advance separately.
			m.appendStreamedContent(strings.Repeat("a", 16))
			m.advanceAnimation()
			got := m.viewState().Reasoning.Text
			if utf8.RuneCountInString(got) != 8 || m.streamShown != 2 {
				t.Fatalf("independent first frame: reasoning=%q assistant=%d", got, m.streamShown)
			}
			out := ansi.Strip(p.Reasoning(m.viewState(), 76))
			if !strings.Contains(out, theme.StreamCursor) {
				t.Fatalf("missing reasoning cursor: %q", out)
			}
			for i := 0; i < 100; i++ {
				m.advanceAnimation()
				if !utf8.ValidString(m.viewState().Reasoning.Text) {
					t.Fatal("split rune")
				}
			}
			if m.viewState().Reasoning.Text != text {
				t.Fatal("reasoning never caught up")
			}
			m.reasoningSince = time.Now().Add(-time.Second)
			for i := range m.messages {
				m.messages[i].arrived = time.Now().Add(-time.Second)
			}
			if m.animating() {
				t.Fatal("caught-up animation did not sleep")
			}
			// A shorter replacement must not reuse an out-of-range byte cut.
			m.reasoning = "é"
			m.advanceAnimation()
			if got := m.viewState().Reasoning.Text; got != "é" || m.reasoningShown != len("é") {
				t.Fatalf("short replacement: %q", got)
			}
			m.endThoughtStep()
			if m.viewState().Reasoning.Text != "" {
				t.Fatal("fold left live text")
			}
			next, _ = m.Update(delta(text))
			m = next.(Model)
			if m.viewState().Reasoning.Text != "" {
				t.Fatal("next step inherited reveal progress")
			}
			m.foldReasoning()
			if got := thoughts(m); got[len(got)-1] != text {
				t.Fatal("fold lost hidden suffix")
			}
			m.advanceAnimation()
			if m.viewState().Reasoning.Text != "" {
				t.Fatal("tick resurrected folded trace")
			}
		})
	}
}

func TestReasoningMotionTurnLifecycle(t *testing.T) {
	for name, end := range map[string]tea.Msg{
		"response":  responseMsg{content: "done"},
		"park":      parkMsg{parked: true, reply: "done"},
		"interrupt": responseMsg{err: context.Canceled},
	} {
		t.Run(name, func(t *testing.T) {
			m := streamModel()
			m.textarea = textarea.New()
			m.turnGen = new(atomic.Int32)
			text := strings.Repeat("完整 thought ", 32)
			next, _ := m.Update(delta(text))
			next, _ = next.(Model).Update(animTickMsg{})
			m = next.(Model)
			if m.reasoningShown == 0 || m.visibleReasoning() == text {
				t.Fatal("expected partial reveal")
			}
			next, _ = m.Update(end)
			m = next.(Model)
			if got := thoughts(m); len(got) != 1 || got[0] != text {
				t.Fatalf("fold lost full text: %q", got)
			}
			if m.reasoningShown != 0 || !m.reasoningSince.IsZero() || m.viewState().Reasoning.Live || m.reasoningArriving() != 0 {
				t.Fatal("motion survived turn end")
			}
			next, _ = m.Update(delta("stale"))
			next, _ = next.(Model).Update(boundary("stale boundary"))
			next, _ = next.(Model).Update(animTickMsg{})
			m = next.(Model)
			if m.reasoning != "" || len(thoughts(m)) != 1 || thoughts(m)[0] != text {
				t.Fatal("late event or tick resurrected reasoning")
			}
			m.loading = true
			next, _ = m.Update(reasoningEventsMsg{{kind: reasoningEventDelta, text: text, gen: m.currentTurnGen()}})
			m = next.(Model)
			if m.visibleReasoning() != "" || !m.reasoningBacklog() {
				t.Fatal("new turn inherited motion")
			}
		})
	}
}

func TestReasoningArrivalFade(t *testing.T) {
	m := streamModel()
	m.reasoning = "thought"
	m.reasoningShown = len(m.reasoning)
	m.reasoningSince = time.Now().Add(-theme.FadeDuration / 2)
	r := m.viewState().Reasoning
	if r.Arriving <= 0 || r.Arriving >= 1 || !m.animating() {
		t.Fatalf("fade-only animation missing: %+v", r)
	}
	if got := m.arriving(ChatMessage{arrived: m.reasoningSince}); got > r.Arriving || r.Arriving-got > 0.1 {
		t.Fatal("reasoning fade differs from assistant fade")
	}
	m.reasoningSince = time.Now().Add(-2 * theme.FadeDuration)
	if m.reasoningArriving() != 0 || m.animating() {
		t.Fatal("expired fade keeps ticking")
	}
	m.clearReasoning()
	if m.reasoningShown != 0 || !m.reasoningSince.IsZero() {
		t.Fatal("clear retained motion")
	}
}
