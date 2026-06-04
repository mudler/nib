package setup

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/types"
)

type step int

const (
	stepProvider step = iota
	stepFields
	stepProbe
	stepSaved
)

const (
	fieldBaseURL = iota
	fieldModel
	fieldAPIKey
)

type model struct {
	ctx context.Context

	step    step
	presets []Preset
	cursor  int

	inputs []textinput.Model
	focus  int

	cfg types.Config

	probing  bool
	probed   bool
	probeErr error

	savedPath string
	saveErr   error
	saved     bool
	quitting  bool
}

type probeResultMsg struct{ err error }

// Run launches the interactive wizard. It returns the resulting config, whether
// it was saved, and any fatal error. Cancellation (Esc/Ctrl+C) returns
// saved=false with a nil error and the unchanged existing config.
func Run(ctx context.Context, existing types.Config) (types.Config, bool, error) {
	p := tea.NewProgram(newModel(ctx, existing))
	res, err := p.Run()
	if err != nil {
		return existing, false, err
	}
	fm, _ := res.(model)
	if fm.saved {
		return fm.cfg, true, nil
	}
	return existing, false, nil
}

func newModel(ctx context.Context, existing types.Config) model {
	mkInput := func(placeholder, val string) textinput.Model {
		ti := textinput.New()
		ti.Placeholder = placeholder
		ti.Prompt = "› "
		ti.SetValue(val)
		return ti
	}
	inputs := []textinput.Model{
		mkInput("https://api.openai.com/v1 (blank = OpenAI default)", existing.BaseURL),
		mkInput("model name", existing.Model),
		mkInput("api key", existing.APIKey),
	}
	inputs[fieldAPIKey].EchoMode = textinput.EchoPassword
	inputs[fieldAPIKey].EchoCharacter = '•'

	return model{
		ctx:     ctx,
		step:    stepProvider,
		presets: Presets(),
		inputs:  inputs,
	}
}

func (m *model) applyPreset(p Preset) {
	m.inputs[fieldBaseURL].SetValue(p.BaseURL)
	m.inputs[fieldModel].SetValue(p.DefaultModel)
	m.inputs[fieldAPIKey].SetValue(p.DefaultKey)
}

func (m *model) focusField(i int) {
	for j := range m.inputs {
		if j == i {
			m.inputs[j].Focus()
		} else {
			m.inputs[j].Blur()
		}
	}
	m.focus = i
}

func (m *model) collect() {
	m.cfg.BaseURL = strings.TrimSpace(m.inputs[fieldBaseURL].Value())
	m.cfg.Model = strings.TrimSpace(m.inputs[fieldModel].Value())
	m.cfg.APIKey = strings.TrimSpace(m.inputs[fieldAPIKey].Value())
}

func (m model) probeCmd() tea.Cmd {
	ctx, cfg := m.ctx, m.cfg
	return func() tea.Msg {
		return probeResultMsg{err: Probe(ctx, cfg.Model, cfg.APIKey, cfg.BaseURL)}
	}
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case probeResultMsg:
		m.probing = false
		m.probed = true
		m.probeErr = msg.err
		return m, nil
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			m.quitting = true
			return m, tea.Quit
		}
		switch m.step {
		case stepProvider:
			return m.updateProvider(msg)
		case stepFields:
			return m.updateFields(msg)
		case stepProbe:
			return m.updateProbe(msg)
		case stepSaved:
			return m.updateSaved(msg)
		}
	}
	return m, nil
}

func (m model) updateProvider(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.quitting = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.presets)-1 {
			m.cursor++
		}
	case "enter":
		m.applyPreset(m.presets[m.cursor])
		m.step = stepFields
		m.focusField(fieldBaseURL)
		return m, textinput.Blink
	}
	return m, nil
}

func (m model) updateFields(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.step = stepProvider
		return m, nil
	case "tab", "down":
		m.focusField((m.focus + 1) % len(m.inputs))
		return m, textinput.Blink
	case "shift+tab", "up":
		m.focusField((m.focus - 1 + len(m.inputs)) % len(m.inputs))
		return m, textinput.Blink
	case "enter":
		m.collect()
		m.step = stepProbe
		m.probing = true
		m.probed = false
		return m, m.probeCmd()
	}
	var cmd tea.Cmd
	m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	return m, cmd
}

func (m model) updateProbe(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.probing {
		return m, nil
	}
	switch msg.String() {
	case "e":
		m.step = stepFields
		m.probed = false
		return m, textinput.Blink
	case "esc":
		m.quitting = true
		return m, tea.Quit
	case "enter", "s":
		path, err := Save(m.cfg)
		m.savedPath, m.saveErr = path, err
		m.saved = err == nil
		m.step = stepSaved
		return m, nil
	}
	return m, nil
}

func (m model) updateSaved(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.saveErr != nil && msg.String() == "e" {
		m.step = stepFields
		m.saved = false
		return m, textinput.Blink
	}
	m.quitting = true
	return m, tea.Quit
}

func (m model) View() string {
	switch m.step {
	case stepProvider:
		return m.viewProvider()
	case stepFields:
		return m.viewFields()
	case stepProbe:
		return m.viewProbe()
	case stepSaved:
		return m.viewSaved()
	}
	return ""
}

func (m model) viewProvider() string {
	var b strings.Builder
	b.WriteString(theme.Brand.Render("nib setup") + "\n")
	b.WriteString(theme.Help.Render("No model configured yet. Pick a provider to get started.") + "\n\n")
	for i, p := range m.presets {
		marker := "( )"
		if i == m.cursor {
			marker = "(•)"
		}
		line := fmt.Sprintf("%s %s", theme.Prompt.Render(marker), p.Name)
		if i == m.cursor {
			line = theme.LabelNib.Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + theme.Hint.Render("↑/↓ move · enter select · esc cancel"))
	return b.String()
}

func (m model) viewFields() string {
	labels := []string{"Base URL", "Model", "API key"}
	var b strings.Builder
	b.WriteString(theme.Brand.Render("nib setup") + "\n")
	b.WriteString(theme.Help.Render("Edit the connection details, then press enter to test.") + "\n\n")
	for i, ti := range m.inputs {
		b.WriteString(theme.LabelYou.Render(labels[i]) + "\n")
		b.WriteString(ti.View() + "\n\n")
	}
	b.WriteString(theme.Hint.Render("tab/↑↓ move · enter test & continue · esc back"))
	return b.String()
}

func (m model) viewProbe() string {
	var b strings.Builder
	b.WriteString(theme.Brand.Render("nib setup") + "\n\n")
	if m.probing {
		b.WriteString(theme.Help.Render("Testing connection…"))
		return b.String()
	}
	if m.probeErr == nil {
		b.WriteString(theme.Done.Render("✓ Connection OK") + "\n")
	} else {
		b.WriteString(theme.Error.Render("⚠ Could not reach the endpoint:") + "\n")
		b.WriteString(theme.Help.Render("  "+m.probeErr.Error()) + "\n")
	}
	b.WriteString("\n" + theme.Hint.Render("enter/s save · e edit · esc cancel"))
	return b.String()
}

func (m model) viewSaved() string {
	var b strings.Builder
	b.WriteString(theme.Brand.Render("nib setup") + "\n\n")
	if m.saveErr != nil {
		b.WriteString(theme.Error.Render("Could not write config: "+m.saveErr.Error()) + "\n\n")
		b.WriteString(theme.Help.Render("Add this to your config manually:") + "\n")
		b.WriteString(fmt.Sprintf("  model: %s\n  api_key: %s\n  base_url: %s\n", m.cfg.Model, m.cfg.APIKey, m.cfg.BaseURL))
		b.WriteString("\n" + theme.Hint.Render("e edit · any key quit"))
		return b.String()
	}
	b.WriteString(theme.Done.Render("✓ Saved to "+m.savedPath) + "\n")
	b.WriteString(theme.Help.Render("Starting nib…"))
	b.WriteString("\n\n" + theme.Hint.Render("press enter to continue"))
	return b.String()
}
