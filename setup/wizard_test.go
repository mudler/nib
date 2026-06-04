package setup

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/types"
)

// Selecting the Ollama preset (index 2) should move to the fields step and
// prefill the base URL and model from that preset.
func TestProviderSelectionPrefillsFields(t *testing.T) {
	m := newModel(context.Background(), types.Config{})

	// Move cursor down twice: OpenAI(0) -> Local(1) -> Ollama(2).
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = mi.(model)
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = mi.(model)

	if m.cursor != 2 {
		t.Fatalf("cursor = %d, want 2", m.cursor)
	}

	// Enter selects the preset and advances to the fields step.
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mi.(model)

	if m.step != stepFields {
		t.Fatalf("step = %d, want stepFields(%d)", m.step, stepFields)
	}
	if got := m.inputs[fieldBaseURL].Value(); got != "http://localhost:11434/v1" {
		t.Errorf("base url = %q", got)
	}
	if got := m.inputs[fieldModel].Value(); got != "llama3.1" {
		t.Errorf("model = %q", got)
	}
}

// Esc on the provider step cancels (no save).
func TestProviderEscCancels(t *testing.T) {
	m := newModel(context.Background(), types.Config{})
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mi.(model)
	if !m.quitting || m.saved {
		t.Fatalf("esc should cancel without saving: quitting=%v saved=%v", m.quitting, m.saved)
	}
}

func TestKeyRequiredTracksPreset(t *testing.T) {
	// OpenAI (index 0) requires a key.
	m := newModel(context.Background(), types.Config{})
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // select index 0
	m = mi.(model)
	if !m.keyRequired {
		t.Errorf("OpenAI preset should set keyRequired=true")
	}

	// Ollama (index 2) does not.
	m2 := newModel(context.Background(), types.Config{})
	for i := 0; i < 2; i++ {
		mi2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyDown})
		m2 = mi2.(model)
	}
	mi2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m2 = mi2.(model)
	if m2.keyRequired {
		t.Errorf("Ollama preset should leave keyRequired=false")
	}
}
