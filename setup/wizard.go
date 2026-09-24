package setup

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/provider"
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

	keyRequired bool

	probing     bool
	probeErr    error
	loginPrompt string
	cancelLogin context.CancelFunc
	startLogin  func(context.Context, *auth.Store, provider.Definition) (*auth.LoginFlow, error)

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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
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
		ti.Prompt = theme.PromptGlyph + " "
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
		ctx:        ctx,
		step:       stepProvider,
		presets:    Presets(),
		inputs:     inputs,
		startLogin: auth.StartLogin,
		// Only the root override carries over from the existing config: the
		// three editable fields come from the inputs (see collect), while Save
		// needs to know which root to write into. Everything else stays zero.
		cfg: types.Config{BaseDir: existing.BaseDir},
	}
}

func (m *model) applyPreset(p Preset) {
	m.cfg.Provider = p.Provider
	if m.cfg.Provider == "" {
		m.cfg.Provider = "openai"
	}
	m.inputs[fieldBaseURL].SetValue(p.BaseURL)
	m.inputs[fieldModel].SetValue(p.DefaultModel)
	m.inputs[fieldAPIKey].SetValue(p.DefaultKey)
	m.keyRequired = p.KeyRequired
}

func (m model) oauth() bool { return m.cfg.Provider == "openai-codex" }

func (m *model) loginCmd() tea.Cmd {
	dir, err := configDirIn(m.cfg.BaseDir)
	if err != nil {
		m.probing, m.probeErr = false, err
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancelLogin = cancel
	def, _ := provider.Get(m.cfg.Provider)
	flow, err := m.startLogin(ctx, auth.NewStore(filepath.Join(dir, "credentials.json")), def)
	if err != nil {
		cancel()
		m.probing, m.probeErr = false, err
		return nil
	}
	m.loginPrompt = flow.Prompt
	return func() tea.Msg {
		defer cancel()
		// The URL remains visible if no browser opener is installed.
		opener := "xdg-open"
		if runtime.GOOS == "darwin" {
			opener = "open"
		}
		if flow.URL != "" {
			cmd := exec.Command(opener, flow.URL)
			if cmd.Start() == nil {
				go cmd.Wait()
			}
		}
		_, err := flow.Complete(ctx)
		return probeResultMsg{err: err}
	}
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
		m.probeErr = msg.err
		return m, nil
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			if m.cancelLogin != nil {
				m.cancelLogin()
			}
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
		if m.oauth() {
			m.focusField(fieldModel)
		}
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
		if m.oauth() {
			return m, nil
		}
		m.focusField((m.focus + 1) % len(m.inputs))
		return m, textinput.Blink
	case "shift+tab", "up":
		if m.oauth() {
			return m, nil
		}
		m.focusField((m.focus - 1 + len(m.inputs)) % len(m.inputs))
		return m, textinput.Blink
	case "enter":
		m.collect()
		m.step = stepProbe
		m.probing = true
		m.probeErr = nil
		if m.oauth() {
			cmd := m.loginCmd()
			return m, cmd
		}
		return m, m.probeCmd()
	}
	var cmd tea.Cmd
	m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	return m, cmd
}

func (m model) updateProbe(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" && m.cancelLogin != nil {
		m.cancelLogin()
		m.quitting = true
		return m, tea.Quit
	}
	if m.probing {
		return m, nil
	}
	switch msg.String() {
	case "e":
		m.step = stepFields
		return m, textinput.Blink
	case "esc":
		m.quitting = true
		return m, tea.Quit
	case "enter", "s":
		if m.oauth() && m.probeErr != nil {
			return m, nil
		}
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
	b.WriteString("\n" + theme.Hint.Render(theme.ScrollKeys+" move · enter select · esc cancel"))
	return b.String()
}

func (m model) viewFields() string {
	labels := []string{"Base URL", "Model", "API key"}
	var b strings.Builder
	b.WriteString(theme.Brand.Render("nib setup") + "\n")
	if m.oauth() {
		b.WriteString(theme.Help.Render("Choose a model, then press enter to sign in with OpenAI in your browser.") + "\n\n")
	} else {
		b.WriteString(theme.Help.Render("Edit the connection details, then press enter to test.") + "\n\n")
	}
	for i, ti := range m.inputs {
		if m.oauth() && i != fieldModel {
			continue
		}
		b.WriteString(theme.LabelYou.Render(labels[i]) + "\n")
		b.WriteString(ti.View() + "\n\n")
	}
	if m.keyRequired && strings.TrimSpace(m.inputs[fieldAPIKey].Value()) == "" {
		b.WriteString(theme.Help.Render("This provider requires an API key.") + "\n\n")
	}
	if m.oauth() {
		b.WriteString(theme.Hint.Render("enter sign in · esc back"))
	} else {
		b.WriteString(theme.Hint.Render("tab/" + theme.ScrollKeys + " move · enter test & continue · esc back"))
	}
	return b.String()
}

func (m model) viewProbe() string {
	var b strings.Builder
	b.WriteString(theme.Brand.Render("nib setup") + "\n\n")
	if m.oauth() {
		if m.probing {
			b.WriteString(m.loginPrompt + "\n\nWaiting for sign-in…\n\nesc cancel")
		} else if m.probeErr != nil {
			b.WriteString(theme.Error.Render("Sign-in failed: "+m.probeErr.Error()) + "\n\ne edit & retry · esc cancel")
		} else {
			b.WriteString(theme.Done.Render("✓ Signed in with OpenAI") + "\n\nenter/s save · e edit · esc cancel")
		}
		return b.String()
	}
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
		b.WriteString(fmt.Sprintf("  provider: %s\n  model: %s\n  api_key: %s\n  base_url: %s\n", m.cfg.Provider, m.cfg.Model, m.cfg.APIKey, m.cfg.BaseURL))
		b.WriteString("\n" + theme.Hint.Render("e edit · any key quit"))
		return b.String()
	}
	b.WriteString(theme.Done.Render("✓ Saved to "+m.savedPath) + "\n")
	b.WriteString(theme.Help.Render("Starting nib…"))
	b.WriteString("\n\n" + theme.Hint.Render("press enter to continue"))
	return b.String()
}
