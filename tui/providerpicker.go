package tui

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/endpoint"
	"github.com/mudler/nib/provider"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
)

// pickerMode selects which rows providerPicker.open lists and what Enter
// does with the one chosen, so the same dialog serves three different verbs.
type pickerMode int

const (
	// pickerLogin is /login: registry providers only, Enter authenticates
	// (or, once ready, moves straight to model selection).
	pickerLogin pickerMode = iota
	// pickerLogout is /logout: only entries with a stored credential, Enter
	// removes it.
	pickerLogout
	// pickerEndpoint is /endpoint: every entry — the config.yaml default, its
	// named endpoints, then the registry — Enter switches to it.
	pickerEndpoint
	// pickerClassifier is /classifier: the config.yaml default and its named
	// endpoints (a registry provider serves no classifier), Enter lists the
	// endpoint's models for the classifier.
	pickerClassifier
)

// providerPicker is the /login, /logout and /endpoint dialog: a searchable
// list of providers (or endpoints) with their state, the same idiom as the
// model picker.
type providerPicker struct {
	active   bool
	mode     pickerMode
	all      []chat.ProviderEntry
	matches  []chat.ProviderEntry
	query    string
	selected int
	offset   int
}

func (p *providerPicker) open(entries []chat.ProviderEntry, mode pickerMode) {
	*p = providerPicker{active: true, mode: mode}
	for _, e := range entries {
		switch mode {
		case pickerLogout:
			if !e.Stored {
				continue
			}
		case pickerLogin:
			if e.Kind != endpoint.KindProvider {
				continue
			}
		case pickerClassifier:
			if e.Kind == endpoint.KindProvider {
				continue
			}
		}
		p.all = append(p.all, e)
	}
	p.filter()
	// Start on the current provider so Enter is a no-op-shaped default.
	for i, e := range p.matches {
		if e.Current {
			p.selected = i
		}
	}
	// Append the "+ add new" row in endpoint mode so Enter on it opens
	// the add-endpoint form.
	if mode == pickerEndpoint {
		p.matches = append(p.matches, chat.ProviderEntry{
			ID:   endpointAddSentinel,
			Name: theme.EndpointAddRow,
			Kind: endpoint.KindNamed,
		})
	}
	p.scrollSelectionIntoView()
}

func (p *providerPicker) close() { *p = providerPicker{} }

func (p *providerPicker) filter() {
	p.matches = p.matches[:0]
	q := strings.ToLower(p.query)
	for _, e := range p.all {
		if strings.Contains(strings.ToLower(e.ID), q) || strings.Contains(strings.ToLower(e.Name), q) {
			p.matches = append(p.matches, e)
		}
	}
	p.selected, p.offset = 0, 0
}

// endpointAddSentinel is the ID of the "+ add new endpoint" row appended to
// the endpoint picker. It is not a real provider, so it is never sent to
// useEndpoint — handleProviderPickerKey intercepts it.
const endpointAddSentinel = "__add__"

func (p *providerPicker) hasEndpointAddRow() bool {
	for _, e := range p.matches {
		if e.ID == endpointAddSentinel {
			return true
		}
	}
	return false
}

func (p *providerPicker) appendQuery(text string) {
	p.query += text
	p.filter()
}

func (p *providerPicker) backspace() {
	if r := []rune(p.query); len(r) > 0 {
		p.query = string(r[:len(r)-1])
		p.filter()
	}
}

func (p *providerPicker) move(delta int) {
	p.selected = max(0, min(p.selected+delta, len(p.matches)-1))
	p.scrollSelectionIntoView()
}

func (p *providerPicker) scrollSelectionIntoView() {
	if p.selected < p.offset {
		p.offset = p.selected
	}
	if p.selected >= p.offset+modelPickerMaxVisible {
		p.offset = p.selected - modelPickerMaxVisible + 1
	}
}

func (p providerPicker) choice() (chat.ProviderEntry, bool) {
	if p.selected < 0 || p.selected >= len(p.matches) {
		return chat.ProviderEntry{}, false
	}
	return p.matches[p.selected], true
}

func (p providerPicker) dialog() render.Dialog {
	title, hint := theme.ProviderPickerTitle, theme.ProviderPickerKeyHint
	switch p.mode {
	case pickerLogout:
		title, hint = theme.ProviderPickerLogoutTitle, theme.ProviderPickerLogoutHint
	case pickerEndpoint:
		title, hint = theme.EndpointPickerTitle, theme.EndpointPickerKeyHint
	case pickerClassifier:
		title, hint = theme.ClassifierPickerTitle, theme.ClassifierPickerKeyHint
	}
	title += " " + theme.ModelPickerSearchLabel
	if p.query != "" {
		title += " " + p.query
	}
	d := render.Dialog{Kind: render.DialogModelPicker, Title: title, Hint: hint}
	if len(p.matches) == 0 {
		d.Hint = theme.ProviderPickerNoMatches
		return d
	}
	end := min(p.offset+modelPickerMaxVisible, len(p.matches))
	for _, e := range p.matches[p.offset:end] {
		text := e.Name
		if e.ID != e.Name && e.ID != chat.ConfigProviderID {
			text += " (" + e.ID + ")"
		}
		text += " · " + e.Status
		if e.Current {
			text += theme.ProviderCurrentSuffix
		}
		d.Options = append(d.Options, render.DialogOption{Text: text})
	}
	d.Selected = p.selected - p.offset
	return d
}

// loginForm collects an API key (and, for providers without a default
// endpoint, a base URL) inside the TUI. Keys are masked on screen.
type loginForm struct {
	active bool
	entry  chat.ProviderEntry
	fields []formField
	focus  int
	err    string
}

type formField struct {
	label  string
	value  []rune
	secret bool
}

func (f *loginForm) open(e chat.ProviderEntry) {
	*f = loginForm{active: true, entry: e, fields: []formField{{label: theme.LoginFormKeyLabel, secret: true}}}
	if e.NeedsBaseURL {
		f.fields = append(f.fields, formField{label: theme.LoginFormURLLabel})
	}
}

func (f *loginForm) close() { *f = loginForm{} }

func (f *loginForm) value(i int) string { return strings.TrimSpace(string(f.fields[i].value)) }

func (f loginForm) dialog() render.Dialog {
	d := render.Dialog{Kind: render.DialogModelPicker, Title: fmt.Sprintf(theme.LoginFormTitle, f.entry.Name), Selected: f.focus}
	for _, fld := range f.fields {
		shown := string(fld.value)
		if fld.secret {
			// A fixed-width mask: the length of a key is nobody's business,
			// and a long one would otherwise wrap the dialog.
			shown = strings.Repeat("•", min(len(fld.value), 24))
		}
		d.Options = append(d.Options, render.DialogOption{Text: fld.label + ": " + shown})
	}
	hint := theme.LoginFormHint
	if len(f.fields) > 1 {
		hint = theme.LoginFormFieldsHint
	}
	if f.entry.EnvVar != "" {
		hint += " · " + fmt.Sprintf(theme.LoginFormEnvHint, f.entry.EnvVar)
	}
	if f.err != "" {
		hint = f.err + " · " + hint
	}
	d.Hint = hint
	return d
}

// loginWait is the dialog shown while an OAuth or device-code flow waits on
// the browser. cancel aborts the flow (closing the callback server).
type loginWait struct {
	active bool
	entry  chat.ProviderEntry
	prompt string // device-code instructions, repeated in the dialog
	cancel context.CancelFunc
}

// loginResultMsg is sent when an in-TUI login flow completes.
type loginResultMsg struct {
	entry chat.ProviderEntry
	cred  auth.Credential
	err   error
}

// openProviderPicker opens /login's picker, /logout's, or /endpoint's. This
// is the one production call site of providerPicker.open; openEndpointPicker
// (tui/endpointpicker.go) routes through it rather than calling open itself.
func (m *Model) openProviderPicker(mode pickerMode) {
	m.providerPicker.open(m.session.Providers(), mode)
	if mode == pickerLogout && len(m.providerPicker.all) == 0 {
		m.providerPicker.close()
		m.appendMessage(ChatMessage{Role: "agent", Content: theme.ProviderNoneLoggedIn})
	}
}

// providerEntry finds a provider by ID for `/login <id>`.
func (m *Model) providerEntry(id string) (chat.ProviderEntry, bool) {
	for _, e := range m.session.Providers() {
		if e.ID == id {
			return e, true
		}
	}
	return chat.ProviderEntry{}, false
}

// useProvider is Enter in the /login picker. A provider that can already
// authenticate goes straight to model selection; anything else logs in first.
// force (an explicit `/login <id>`) logs in again even when a key exists, which
// is how a rotated key gets replaced.
//
// Every caller now hands it a registry entry only (KindProvider): the /login
// picker's list is filtered to the registry, useEndpoint routes a
// KindProvider row here and everything else itself, and the KindLogin
// dispatch in tui/model.go rejects a non-KindProvider `/login <id>` before
// reaching this function. config.yaml's own default and named entries switch
// through /endpoint (useEndpoint) instead.
func (m *Model) useProvider(e chat.ProviderEntry, force bool) tea.Cmd {
	if e.LoginKind == provider.LoginNone || (e.Ready && !force) {
		return m.openProviderModelPicker(e)
	}
	return m.startLogin(e)
}

// startLogin runs the flow e's login kind calls for, entirely in the TUI.
func (m *Model) startLogin(e chat.ProviderEntry) tea.Cmd {
	if e.LoginKind == provider.LoginAPIKey {
		m.loginForm.open(e)
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	flow, err := m.session.StartLogin(ctx, e.ID)
	if err != nil {
		cancel()
		m.appendMessage(ChatMessage{Role: "error", Content: "Login failed: " + err.Error()})
		return nil
	}
	if flow.URL != "" {
		// Keep the URL in the transcript: the browser may not open (SSH,
		// headless), and the user must be able to copy it.
		m.appendMessage(ChatMessage{Role: "agent", Content: flow.Prompt})
		openBrowser(flow.URL)
	}
	m.loginWait = loginWait{active: true, entry: e, cancel: cancel}
	if e.LoginKind == provider.LoginDeviceCode {
		m.loginWait.prompt = flow.Prompt
	}
	return func() tea.Msg {
		defer cancel()
		cred, err := flow.Complete(ctx)
		return loginResultMsg{entry: e, cred: cred, err: err}
	}
}

// submitLoginForm saves the typed key and moves on to model selection.
func (m *Model) submitLoginForm() tea.Cmd {
	f := &m.loginForm
	if f.value(0) == "" {
		f.err = "the API key is empty"
		return nil
	}
	baseURL := ""
	if f.entry.NeedsBaseURL {
		baseURL = f.value(1)
		if baseURL == "" {
			f.err = "this provider needs a base URL"
			f.focus = 1
			return nil
		}
	}
	cred, err := m.session.SaveAPIKey(f.entry.ID, f.value(0), baseURL)
	if err != nil {
		f.err = err.Error()
		return nil
	}
	e := f.entry
	f.close()
	return m.loggedIn(e, cred)
}

func (m *Model) loggedIn(e chat.ProviderEntry, cred auth.Credential) tea.Cmd {
	m.appendMessage(ChatMessage{Role: "agent", Content: fmt.Sprintf("Logged in to %s (%s)", e.Name, cred.DisplayLabel())})
	e.Ready, e.Stored = true, true
	return m.openProviderModelPicker(e)
}

// handleLoginResult finishes an OAuth/device/Copilot flow.
func (m *Model) handleLoginResult(msg loginResultMsg) tea.Cmd {
	if !m.loginWait.active || m.loginWait.entry.ID != msg.entry.ID {
		return nil // cancelled; a late result for a flow nobody waits on
	}
	m.loginWait = loginWait{}
	if msg.err != nil {
		m.appendMessage(ChatMessage{Role: "error", Content: "Login failed: " + msg.err.Error()})
		return nil
	}
	return m.loggedIn(msg.entry, msg.cred)
}

func (m *Model) cancelLoginWait() {
	if m.loginWait.cancel != nil {
		m.loginWait.cancel()
	}
	m.loginWait = loginWait{}
	m.appendMessage(ChatMessage{Role: "agent", Content: theme.LoginCancelled})
}

func (w loginWait) dialog() render.Dialog {
	d := render.Dialog{Kind: render.DialogModelPicker, Title: fmt.Sprintf(theme.LoginWaitTitle, w.entry.Name), Hint: theme.LoginWaitHint}
	if w.prompt != "" {
		d.Options = []render.DialogOption{{Text: w.prompt}}
		d.Selected = -1
	}
	return d
}

// logout is Enter in the /logout picker.
func (m *Model) logout(id string) {
	notice, err := m.session.Logout(id)
	if err != nil {
		m.appendMessage(ChatMessage{Role: "error", Content: err.Error()})
		return
	}
	if m.session.ProviderID() == id {
		notice += " · this session keeps its connection until you switch provider (/login)"
	}
	m.appendMessage(ChatMessage{Role: "agent", Content: notice})
}

// handleProviderPickerKey drives the /login and /logout picker.
func (m Model) handleProviderPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	p := &m.providerPicker
	switch msg.Type {
	case tea.KeyEsc:
		p.close()
	case tea.KeyUp:
		p.move(-1)
	case tea.KeyDown:
		p.move(1)
	case tea.KeyPgUp:
		p.move(-modelPickerMaxVisible)
	case tea.KeyPgDown:
		p.move(modelPickerMaxVisible)
	case tea.KeyBackspace:
		p.backspace()
	case tea.KeyEnter:
		if e, ok := p.choice(); ok {
			mode := p.mode
			p.close()
			switch mode {
			case pickerLogout:
				m.logout(e.ID)
			case pickerEndpoint:
				if e.ID == endpointAddSentinel {
					m.openEndpointForm()
					return m, nil
				}
				cmd = m.useEndpoint(e)
			case pickerClassifier:
				cmd = m.openClassifierModelPicker(e)
			default:
				cmd = m.useProvider(e, false)
			}
		}
	case tea.KeySpace:
		p.appendQuery(" ")
	case tea.KeyRunes:
		if s := printableRunes(msg.Runes); s != "" {
			p.appendQuery(s)
		}
	}
	m.updateViewport()
	return m, cmd
}

// handleLoginFormKey drives the API-key form.
func (m Model) handleLoginFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	f := &m.loginForm
	fld := &f.fields[f.focus]
	switch msg.Type {
	case tea.KeyEsc:
		f.close()
		m.appendMessage(ChatMessage{Role: "agent", Content: theme.LoginCancelled})
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
			cmd = m.submitLoginForm()
		}
	case tea.KeyRunes, tea.KeySpace:
		// Pastes arrive as one KeyRunes; keys never contain spaces, but a
		// URL typed with one would be rejected by the server, not us.
		if s := printableRunes(msg.Runes); s != "" {
			fld.value = append(fld.value, []rune(strings.TrimSpace(s))...)
			f.err = ""
		}
	}
	m.updateViewport()
	return m, cmd
}

func printableRunes(rs []rune) string {
	out := make([]rune, 0, len(rs))
	for _, r := range rs {
		if unicode.IsPrint(r) {
			out = append(out, r)
		}
	}
	return string(out)
}
