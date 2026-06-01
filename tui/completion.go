package tui

import (
	"fmt"
	"strings"

	"github.com/mudler/wiz/types"

	"github.com/charmbracelet/lipgloss"
)

// compCategory tags a completion item by source registry.
type compCategory string

const (
	compCmd   compCategory = "cmd"
	compSkill compCategory = "skill"
	compAgent compCategory = "agent"
)

// compItem is one entry in the unified `/` completion list.
type compItem struct {
	Cat    compCategory
	Name   string
	Desc   string
	Insert string // canonical token placed in the input on accept (trailing space)
}

// buildCompItems flattens the three registries into tagged completion items.
func buildCompItems(cmds []types.CommandConfig, skills []types.Skill, agents []types.AgentTypeConfig) []compItem {
	items := make([]compItem, 0, len(cmds)+len(skills)+len(agents))
	for _, c := range cmds {
		items = append(items, compItem{Cat: compCmd, Name: c.Name, Desc: c.Description, Insert: "/" + c.Name + " "})
	}
	for _, s := range skills {
		items = append(items, compItem{Cat: compSkill, Name: s.Name, Desc: s.Description, Insert: "/skill " + s.Name + " "})
	}
	for _, a := range agents {
		items = append(items, compItem{Cat: compAgent, Name: a.Name, Desc: a.Description, Insert: "/agent " + a.Name + " "})
	}
	return items
}

// filterComp returns items whose name contains the query (case-insensitive
// substring). An empty query returns all items. Order is preserved.
func filterComp(items []compItem, query string) []compItem {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		out := make([]compItem, len(items))
		copy(out, items)
		return out
	}
	var out []compItem
	for _, it := range items {
		if strings.Contains(strings.ToLower(it.Name), q) {
			out = append(out, it)
		}
	}
	return out
}

// compState holds the live `/` completion popup state.
type compState struct {
	all     []compItem
	active  bool
	matches []compItem
	sel     int
}

// setRegistries seeds the completion source from the three registries.
func (c *compState) setRegistries(cmds []types.CommandConfig, skills []types.Skill, agents []types.AgentTypeConfig) {
	c.all = buildCompItems(cmds, skills, agents)
}

// sync recomputes active/matches from the current input. The popup is active
// while the user is still typing the verb: input starts with '/' and contains
// no space yet. Once a space is typed (args begin) it deactivates.
func (c *compState) sync(input string) {
	if !strings.HasPrefix(input, "/") || strings.ContainsAny(input, " \t") {
		c.active = false
		c.matches = nil
		c.sel = 0
		return
	}
	c.active = true
	c.matches = filterComp(c.all, input[1:])
	if len(c.matches) == 0 {
		c.active = false
	}
	if c.sel >= len(c.matches) {
		c.sel = 0
	}
}

func (c *compState) up() {
	if c.sel > 0 {
		c.sel--
	}
}

func (c *compState) down() {
	if c.sel < len(c.matches)-1 {
		c.sel++
	}
}

// current returns the selected match.
func (c *compState) current() (compItem, bool) {
	if !c.active || c.sel < 0 || c.sel >= len(c.matches) {
		return compItem{}, false
	}
	return c.matches[c.sel], true
}

// accept returns the selected item's Insert token.
func (c *compState) accept() (string, bool) {
	it, ok := c.current()
	if !ok {
		return "", false
	}
	return it.Insert, true
}

// ghost returns the suffix of the selected item's Insert beyond the current
// input (the dim hint shown to the user). Empty if no clean continuation.
func (c *compState) ghost(input string) string {
	it, ok := c.current()
	if !ok {
		return ""
	}
	if strings.HasPrefix(it.Insert, input) {
		return it.Insert[len(input):]
	}
	return ""
}

// renderCompletion renders the popup: a tagged, selectable list plus a ghost hint.
func renderCompletion(c compState, input string, width int) string {
	if !c.active || len(c.matches) == 0 {
		return ""
	}
	var b strings.Builder
	for i, it := range c.matches {
		tag := completionTagStyle.Render(fmt.Sprintf("[%s]", it.Cat))
		line := fmt.Sprintf("%s %-16s %s", tag, it.Name, dimmedStyle.Render(it.Desc))
		if i == c.sel {
			line = completionSelectedStyle.Render("▸ ") + line
		} else {
			line = "  " + line
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	if g := c.ghost(input); g != "" {
		b.WriteString(dimmedStyle.Render("Tab → " + input + g))
		b.WriteString("\n")
	}
	return completionBoxStyle.Width(width).Render(strings.TrimRight(b.String(), "\n"))
}

// completion styles (kept here so the engine file is self-contained).
var (
	completionBoxStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	completionTagStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	completionSelectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
)
