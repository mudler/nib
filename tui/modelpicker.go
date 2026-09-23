package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render"
)

// modelPickerMaxVisible is the most model rows the dialog shows at once.
// The list scrolls within this window, biased to keep the selection visible.
const modelPickerMaxVisible = 8

type modelPicker struct {
	active    bool
	loading   bool
	requestID uint64
	// target is the provider being switched to (a /login pick), or nil for
	// the session's current provider (/model).
	target *chat.ProviderEntry
	// forClassifier means the pick sets the session's classifier model on
	// target (/classifier), not the chat model.
	forClassifier bool
	// typed means no model list is available for target: the query itself
	// is the model name Enter uses.
	typed bool
	// partial means the list is only a suggestion (built-in or mapped), so
	// a typed name outside it is accepted like for a provider switch.
	partial  bool
	listErr  string
	all      []string
	matches  []string
	query    string
	selected int
	offset   int
}

func (p *modelPicker) open(requestID uint64) {
	*p = modelPicker{active: true, loading: true, requestID: requestID}
}

func (p *modelPicker) close() {
	*p = modelPicker{}
}

func (p *modelPicker) setModels(models []string, current string) {
	p.loading = false
	p.all = append([]string(nil), models...)
	p.matches = append([]string(nil), models...)
	p.selected = 0
	p.offset = 0
	for i, model := range p.matches {
		if model == current {
			p.selected = i
			break
		}
	}
	p.scrollSelectionIntoView(modelPickerMaxVisible)
}

func (p *modelPicker) appendQuery(text string) {
	p.query += text
	p.filter()
}

func (p *modelPicker) backspace() {
	runes := []rune(p.query)
	if len(runes) == 0 {
		return
	}
	p.query = string(runes[:len(runes)-1])
	p.filter()
}

func (p *modelPicker) filter() {
	p.matches = p.matches[:0]
	query := strings.ToLower(p.query)
	for _, model := range p.all {
		if strings.Contains(strings.ToLower(model), query) {
			p.matches = append(p.matches, model)
		}
	}
	p.selected = 0
	p.offset = 0
	p.scrollSelectionIntoView(modelPickerMaxVisible)
}

func (p *modelPicker) move(delta int) {
	if len(p.matches) == 0 {
		p.selected = 0
		p.offset = 0
		return
	}
	p.selected += delta
	if p.selected < 0 {
		p.selected = 0
	}
	if p.selected >= len(p.matches) {
		p.selected = len(p.matches) - 1
	}
	p.scrollSelectionIntoView(modelPickerMaxVisible)
}

func (p *modelPicker) scrollSelectionIntoView(visible int) {
	if visible < 1 {
		visible = 1
	}
	if p.selected < p.offset {
		p.offset = p.selected
	}
	if p.selected >= p.offset+visible {
		p.offset = p.selected - visible + 1
	}
	if p.offset < 0 {
		p.offset = 0
	}
}

func (p modelPicker) choice() (string, bool) {
	if p.selected < 0 || p.selected >= len(p.matches) {
		// A provider being switched to may serve models its list omits, and
		// some have no list at all: Enter takes the typed name as-is. /model
		// keeps refusing unlisted names (see Session.SwitchModel).
		if (p.typed || p.partial || p.target != nil) && strings.TrimSpace(p.query) != "" {
			return strings.TrimSpace(p.query), true
		}
		return "", false
	}
	return p.matches[p.selected], true
}

// window returns the [start, end) slice of matches currently visible in the
// dialog, clamped to modelPickerMaxVisible and offset.
func (p modelPicker) window() (start, end int) {
	n := len(p.matches)
	if n == 0 {
		return 0, 0
	}
	start = p.offset
	if start < 0 {
		start = 0
	}
	if start >= n {
		start = n - 1
	}
	end = start + modelPickerMaxVisible
	if end > n {
		end = n
	}
	return start, end
}

// buildModelPickerDialog produces the render.Dialog for the model picker,
// mirroring buildResumeDialog: the search query is the Title, the visible
// window of filtered matches are the Options, and the key hint (or a
// loading/empty/no-match message) is the Hint.
func (m Model) buildModelPickerDialog() render.Dialog {
	p := m.modelPicker
	current := ""
	if m.session != nil && p.target == nil {
		current = m.session.Model()
	}

	search := theme.ModelPickerSearchLabel
	if p.typed {
		search = theme.ModelPickerNameLabel
	}
	// The title always names the provider whose models are listed: /model
	// only offers the current provider's, and without its name a /login pick
	// and config.yaml's endpoint look the same.
	switch {
	case p.target != nil && p.forClassifier:
		search = theme.ClassifierPickerTitle + " " + p.target.Name + " model " + search
	case p.target != nil:
		search = p.target.Name + " model " + search
	case m.session != nil:
		search = m.session.ActiveProviderName() + " model " + search
	}
	if p.query != "" {
		search += " " + p.query
	}

	d := render.Dialog{
		Kind:  render.DialogModelPicker,
		Title: search,
		Hint:  theme.ModelPickerKeyHint,
	}
	if p.target == nil {
		// /model cannot change provider; say where that lives. A /login pick
		// (target set) is already the provider switch.
		d.Hint += " · " + theme.ModelPickerLoginHint
	}

	switch {
	case p.loading:
		d.Hint = theme.ModelPickerLoading
	case p.typed:
		d.Hint = theme.ModelPickerTypeName
		if p.listErr != "" {
			d.Hint = p.listErr + " · " + d.Hint
		}
	case len(p.all) == 0:
		d.Hint = theme.ModelPickerEmpty
	case len(p.matches) == 0 && (p.target != nil || p.partial):
		d.Hint = theme.ModelPickerNoMatches + " " + theme.ModelPickerUseTyped
	case len(p.matches) == 0:
		d.Hint = theme.ModelPickerNoMatches
	default:
		start, end := p.window()
		for i := start; i < end; i++ {
			text := p.matches[i]
			if p.matches[i] == current {
				text += " (current)"
			}
			d.Options = append(d.Options, render.DialogOption{Text: text})
		}
		d.Selected = p.selected - start
	}

	return d
}

// modelSwitchNotice confirms a /model pick. On a /login provider or a named
// endpoint, SetModel also saved the model to provider.json, so the notice
// names the endpoint and says it is the new default: the pick outlives the
// session, and the user should not have to discover that from the next
// start's header.
//
// On config.yaml's own DEFAULT endpoint, SavesModelAsDefault is false (see
// its doc), but SetModel still persists the pick there too, uniformly across
// every endpoint. When that pick differs from what config.yaml declares, it
// silently outranks config.yaml on the next start exactly like a /login
// pick would — so this appends the same kind of note the boot log already
// gives that case (tui/boot.go's bootModel), instead of leaving the user to
// find out only then.
func (m Model) modelSwitchNotice(model string) string {
	if m.session == nil {
		return "model: " + model
	}
	if !m.session.SavesModelAsDefault() {
		notice := "model: " + model
		if cfgModel := m.session.ConfigModel(); cfgModel != "" && cfgModel != model {
			notice += " · " + fmt.Sprintf(theme.ModelOverridesConfigNotice, cfgModel)
		}
		return notice
	}
	return "provider: " + m.session.ActiveProviderName() + " · model: " + model + " · " + theme.ProviderSavedDefault
}

func clipModelPickerLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(line) <= width {
		return line
	}
	if width == 1 {
		return "…"
	}

	var b strings.Builder
	used := 0
	for _, r := range line {
		runeWidth := lipgloss.Width(string(r))
		if used+runeWidth > width-1 {
			break
		}
		b.WriteRune(r)
		used += runeWidth
	}
	b.WriteRune('…')
	return b.String()
}
