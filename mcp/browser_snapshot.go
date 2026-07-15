package mcp

import (
	"fmt"
	"regexp"
	"strings"
)

type axNode struct {
	NodeID    string
	Role      string
	Name      string
	BackendID int64
	ChildIDs  []string
	Ignored   bool
}

// interactiveRoles get a clickable/typeable @ref. Others are shown for context
// (headings/text nested in the outline) but only these are addressable.
var interactiveRoles = map[string]bool{
	"link": true, "button": true, "textbox": true, "searchbox": true,
	"checkbox": true, "radio": true, "combobox": true, "listbox": true,
	"option": true, "menuitem": true, "tab": true, "switch": true,
	"slider": true, "textarea": true, "spinbutton": true,
}

// contextRoles are shown in the outline (so the model can read the page) but
// don't get an actionable @ref.
var contextRoles = map[string]bool{
	"heading": true, "StaticText": true, "text": true, "image": true,
	"paragraph": true, "list": true, "listitem": true, "article": true, "main": true,
}

// buildSnapshot walks the AX tree depth-first, emits a compact indented outline,
// assigns @eN refs to interactive nodes, and returns the ref→backendDOMNodeID map
// (refs are per-snapshot; the caller replaces its map each snapshot).
func buildSnapshot(nodes []axNode, compact bool) (string, map[string]int64) {
	byID := make(map[string]axNode, len(nodes))
	for _, n := range nodes {
		byID[n.NodeID] = n
	}
	// Find roots: nodes not referenced as anyone's child.
	child := map[string]bool{}
	for _, n := range nodes {
		for _, c := range n.ChildIDs {
			child[c] = true
		}
	}
	refs := map[string]int64{}
	var b strings.Builder
	next := 0
	var walk func(id string, depth int)
	walk = func(id string, depth int) {
		n, ok := byID[id]
		if !ok {
			return
		}
		role := n.Role
		show := !n.Ignored && role != "" &&
			(interactiveRoles[role] || (!compact && contextRoles[role]) ||
				(compact && contextRoles[role] && n.Name != ""))
		if show {
			indent := strings.Repeat("  ", depth)
			if interactiveRoles[role] {
				next++
				ref := fmt.Sprintf("@e%d", next)
				refs[ref] = n.BackendID
				fmt.Fprintf(&b, "%s%s %s %q\n", indent, ref, role, n.Name)
			} else {
				fmt.Fprintf(&b, "%s%s %q\n", indent, role, n.Name)
			}
			depth++
		}
		for _, c := range n.ChildIDs {
			walk(c, depth)
		}
	}
	for _, n := range nodes {
		if !child[n.NodeID] {
			walk(n.NodeID, 0)
		}
	}
	return b.String(), refs
}

var refRe = regexp.MustCompile(`@e\d+`)

func refTokens(s string) []string { return refRe.FindAllString(s, -1) }
