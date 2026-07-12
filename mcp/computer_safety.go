// ~/_git/wiz/mcp/computer_safety.go
package mcp

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ComputerUseInput is the single wrapper tool's argument set.
type ComputerUseInput struct {
	Action         string   `json:"action" jsonschema:"the action: capture,click,double_click,right_click,middle_click,drag,scroll,type,key,set_value,wait,list_apps,focus_app"`
	Mode           string   `json:"mode,omitempty" jsonschema:"capture mode: som (default),vision,ax"`
	App            string   `json:"app,omitempty" jsonschema:"limit to an app by name or bundle id; 'screen' for the desktop"`
	MaxElements    int      `json:"max_elements,omitempty" jsonschema:"cap on returned AX elements (default 100, max 1000)"`
	Element        int      `json:"element,omitempty" jsonschema:"1-based SOM index from the last capture (preferred over coordinates)"`
	Coordinate     []int    `json:"coordinate,omitempty" jsonschema:"pixel [x,y]; use only when no element index is available"`
	Button         string   `json:"button,omitempty" jsonschema:"left,right,middle"`
	Modifiers      []string `json:"modifiers,omitempty" jsonschema:"modifier keys held during the action"`
	FromElement    int      `json:"from_element,omitempty"`
	ToElement      int      `json:"to_element,omitempty"`
	FromCoordinate []int    `json:"from_coordinate,omitempty"`
	ToCoordinate   []int    `json:"to_coordinate,omitempty"`
	Direction      string   `json:"direction,omitempty" jsonschema:"up,down,left,right"`
	Amount         int      `json:"amount,omitempty" jsonschema:"scroll wheel ticks (default 3)"`
	Value          string   `json:"value,omitempty" jsonschema:"value for set_value"`
	Text           string   `json:"text,omitempty" jsonschema:"text to type"`
	Keys           string   `json:"keys,omitempty" jsonschema:"key combo, e.g. cmd+s, ctrl+alt+t, return"`
	Seconds        float64  `json:"seconds,omitempty" jsonschema:"seconds to wait (max 30)"`
	RaiseWindow    bool     `json:"raise_window,omitempty"`
	CaptureAfter   bool     `json:"capture_after,omitempty" jsonschema:"take a follow-up capture after the action"`
}

var safeComputerActions = map[string]bool{"capture": true, "wait": true, "list_apps": true}

// IsDestructiveComputerAction reports whether an action mutates user-visible
// state and must go through approval. Exported for consumers' approval policies.
func IsDestructiveComputerAction(action string) bool {
	return !safeComputerActions[action] && action != ""
}

var keyAliases = map[string]string{
	"command": "cmd", "control": "ctrl", "alt": "option", "⌘": "cmd", "⌥": "option",
	"windows": "win", "super": "win", "meta": "win",
}

func canonKeyCombo(keys string) map[string]bool {
	out := map[string]bool{}
	for _, p := range strings.Split(keys, "+") {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if a, ok := keyAliases[p]; ok {
			p = a
		}
		out[p] = true
	}
	return out
}

var blockedKeyCombos = [][]string{
	{"cmd", "shift", "backspace"}, {"cmd", "option", "backspace"},
	{"cmd", "ctrl", "q"}, {"cmd", "shift", "q"}, {"cmd", "option", "shift", "q"},
	{"win", "l"}, {"ctrl", "option", "delete"}, {"ctrl", "option", "del"}, {"option", "f4"},
}

var blockedTypePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)curl\s+[^|]*\|\s*bash`),
	regexp.MustCompile(`(?i)curl\s+[^|]*\|\s*sh`),
	regexp.MustCompile(`(?i)wget\s+[^|]*\|\s*bash`),
	regexp.MustCompile(`(?i)\bsudo\s+rm\s+-[rf]`),
	regexp.MustCompile(`(?i)\brm\s+-rf\s+/\s*$`),
	regexp.MustCompile(`(?i):\s*\(\)\s*\{\s*:\|:\s*&\s*\}`),
}

// BlockedComputerReason returns a non-empty reason if the action is hard-blocked
// (un-overridable, regardless of approval). Empty means allowed.
func BlockedComputerReason(in ComputerUseInput) string {
	if in.Action == "key" && in.Keys != "" {
		combo := canonKeyCombo(in.Keys)
		for _, blocked := range blockedKeyCombos {
			all := true
			for _, k := range blocked {
				if !combo[k] {
					all = false
					break
				}
			}
			if all {
				sort.Strings(blocked)
				return fmt.Sprintf("blocked key combo: %s", strings.Join(blocked, "+"))
			}
		}
	}
	if in.Action == "type" && in.Text != "" {
		for _, pat := range blockedTypePatterns {
			if pat.MatchString(in.Text) {
				return fmt.Sprintf("blocked pattern in typed text: %s", pat.String())
			}
		}
	}
	return ""
}
