package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
	"github.com/mudler/nib/types"
)

// endpointTemplate is a preset that pre-fills form fields.
type endpointTemplate struct {
	name     string
	provider string
	baseURL  string
}

// endpointTemplates is the ordered list shown in the template picker.
var endpointTemplates = []endpointTemplate{
	{name: "OpenAI", provider: "openai", baseURL: "https://api.openai.com/v1"},
	{name: "OpenAI Responses", provider: "openai-responses", baseURL: "https://api.openai.com/v1"},
	{name: "OpenRouter", provider: "openai", baseURL: "https://openrouter.ai/api/v1"},
	{name: "LocalAI", provider: "openai", baseURL: "http://localhost:8080/v1"},
	{name: "Ollama", provider: "ollama", baseURL: "http://localhost:11434"},
	{name: "Custom", provider: "", baseURL: ""},
}

// endpointFormState drives the two-phase add-endpoint dialog: first a
// template picker, then a form for the remaining fields. active is true
// across both phases; templatePhase is true during the picker, false
// during the form.
type endpointFormState struct {
	active        bool
	templatePhase bool
	selected      int // template picker cursor
	fields        []formField
	focus         int
	err           string
}

func (f *endpointFormState) openTemplate() {
	*f = endpointFormState{active: true, templatePhase: true, selected: 0}
}

func (f *endpointFormState) openForm(tmpl endpointTemplate) {
	*f = endpointFormState{
		active: true,
		fields: []formField{
			{label: theme.EndpointFieldNameLabel},
			{label: theme.EndpointFormURLLabel, value: []rune(tmpl.baseURL)},
			{label: theme.EndpointFormModelLabel},
			{label: theme.EndpointFormKeyLabel, secret: true},
		},
		focus: 0,
	}
}

func (f *endpointFormState) close() { *f = endpointFormState{} }

func (f *endpointFormState) value(i int) string { return strings.TrimSpace(string(f.fields[i].value)) }

// dialog renders the template picker or the form, depending on the phase.
func (f endpointFormState) dialog() render.Dialog {
	if f.templatePhase {
		return f.templateDialog()
	}
	return f.formDialog()
}

func (f endpointFormState) templateDialog() render.Dialog {
	d := render.Dialog{Kind: render.DialogModelPicker, Title: theme.EndpointAddTitle, Hint: theme.EndpointAddHint}
	for _, tmpl := range endpointTemplates {
		text := tmpl.name
		if tmpl.provider != "" {
			text += " · " + tmpl.provider
		}
		d.Options = append(d.Options, render.DialogOption{Text: text})
	}
	d.Selected = f.selected
	return d
}

func (f endpointFormState) formDialog() render.Dialog {
	tmplName := endpointTemplates[f.selected].name
	d := render.Dialog{Kind: render.DialogModelPicker, Title: fmt.Sprintf(theme.EndpointFormTitle, tmplName), Selected: f.focus}
	for _, fld := range f.fields {
		shown := string(fld.value)
		if fld.secret {
			shown = strings.Repeat("•", min(len(fld.value), 24))
		}
		d.Options = append(d.Options, render.DialogOption{Text: fld.label + ": " + shown})
	}
	hint := theme.EndpointFormHint
	if f.err != "" {
		hint = f.err + " · " + hint
	}
	d.Hint = hint
	return d
}

// openEndpointForm starts the add-endpoint dialog at the template picker.
func (m *Model) openEndpointForm() {
	m.endpointForm.openTemplate()
	m.updateViewport()
}

// handleEndpointFormKey drives both phases of the add-endpoint dialog.
func (m Model) handleEndpointFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	f := &m.endpointForm
	if f.templatePhase {
		return m.handleEndpointTemplateKey(msg)
	}
	// Form phase — same navigation as handleLoginFormKey.
	fld := &f.fields[f.focus]
	switch msg.Type {
	case tea.KeyEsc:
		f.close()
	case tea.KeyTab, tea.KeyDown:
		f.focus = (f.focus + 1) % len(f.fields)
	case tea.KeyShiftTab, tea.KeyUp:
		f.focus = (f.focus + len(f.fields) - 1) % len(f.fields)
	case tea.KeyBackspace:
		if len(fld.value) > 0 {
			fld.value = fld.value[:len(fld.value)-1]
		}
	case tea.KeyCtrlU:
		fld.value = nil
	case tea.KeyEnter:
		if f.focus < len(f.fields)-1 && f.value(f.focus) != "" {
			f.focus++
		} else {
			cmd = m.submitEndpointForm()
		}
	case tea.KeyRunes, tea.KeySpace:
		if s := printableRunes(msg.Runes); s != "" {
			fld.value = append(fld.value, []rune(strings.TrimSpace(s))...)
			f.err = ""
		}
	}
	m.updateViewport()
	return m, cmd
}

func (m Model) handleEndpointTemplateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.endpointForm
	switch msg.Type {
	case tea.KeyEsc:
		f.close()
	case tea.KeyUp:
		if f.selected > 0 {
			f.selected--
		}
	case tea.KeyDown:
		if f.selected < len(endpointTemplates)-1 {
			f.selected++
		}
	case tea.KeyEnter:
		tmpl := endpointTemplates[f.selected]
		f.openForm(tmpl)
	}
	m.updateViewport()
	return m, nil
}

// submitEndpointForm validates the form fields, persists the endpoint via
// the session, and closes the dialog.
func (m *Model) submitEndpointForm() tea.Cmd {
	f := &m.endpointForm
	name := f.value(0)
	baseURL := f.value(1)
	model := f.value(2)
	apiKey := f.value(3)

	if name == "" {
		f.err = "name is required"
		f.focus = 0
		return nil
	}
	if baseURL == "" {
		f.err = "base URL is required"
		f.focus = 1
		return nil
	}

	tmpl := endpointTemplates[f.selected]
	cfg := types.ModelProviderConfig{
		Provider: tmpl.provider,
		BaseURL:  baseURL,
		Model:    model,
		APIKey:   apiKey,
	}
	if cfg.Provider == "" {
		// Custom template: let the endpoint set its own provider from
		// what was typed (or leave it empty for a pure base_url endpoint).
		cfg.Provider = ""
	}

	if err := m.session.AddEndpoint(name, cfg); err != nil {
		f.err = err.Error()
		return nil
	}

	f.close()
	m.appendMessage(ChatMessage{Role: "agent", Content: fmt.Sprintf(theme.EndpointAdded, name, name)})
	return nil
}
