// ~/_git/wiz/mcp/computer_map.go
package mcp

import (
	"fmt"
	"strings"
)

// StickyContext is the pid/window/session carried forward from the last capture.
type StickyContext struct {
	PID       int
	WindowID  int
	SessionID string
}

var modifierKeys = map[string]bool{
	"cmd": true, "shift": true, "option": true, "alt": true, "ctrl": true,
	"fn": true, "win": true, "windows": true, "super": true, "meta": true,
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// buildCuaCall translates one wrapper action into a cua-driver tool name + args.
// Mutating actions require a prior capture/focus_app to have set pid.
func buildCuaCall(in ComputerUseInput, ctx StickyContext) (string, map[string]any, error) {
	base := map[string]any{}
	if ctx.SessionID != "" {
		base["session"] = ctx.SessionID
	}
	mutating := IsDestructiveComputerAction(in.Action)
	if mutating && in.Action != "focus_app" && ctx.PID == 0 {
		return "", nil, fmt.Errorf("no active window — call capture() first")
	}
	withCtx := func(m map[string]any) map[string]any {
		if ctx.PID != 0 {
			m["pid"] = ctx.PID
		}
		if ctx.WindowID != 0 {
			m["window_id"] = ctx.WindowID
		}
		for k, v := range base {
			m[k] = v
		}
		return m
	}

	switch in.Action {
	case "click", "double_click", "right_click", "middle_click":
		tool := "click"
		if in.Action == "double_click" {
			tool = "double_click"
		}
		button := in.Button
		if button == "" {
			switch in.Action {
			case "right_click":
				button = "right"
			case "middle_click":
				button = "middle"
			default:
				button = "left"
			}
		}
		m := map[string]any{"button": button}
		if in.Element != 0 {
			m["element_index"] = in.Element
		} else if len(in.Coordinate) == 2 {
			m["x"], m["y"] = in.Coordinate[0], in.Coordinate[1]
		}
		if len(in.Modifiers) > 0 {
			m["modifier"] = in.Modifiers
		}
		return tool, withCtx(m), nil
	case "drag":
		m := map[string]any{}
		if in.FromElement != 0 || in.ToElement != 0 {
			m["from_element"], m["to_element"] = in.FromElement, in.ToElement
		} else if len(in.FromCoordinate) == 2 && len(in.ToCoordinate) == 2 {
			m["from_x"], m["from_y"] = in.FromCoordinate[0], in.FromCoordinate[1]
			m["to_x"], m["to_y"] = in.ToCoordinate[0], in.ToCoordinate[1]
		}
		return "drag", withCtx(m), nil
	case "scroll":
		amount := in.Amount
		if amount == 0 {
			amount = 3
		}
		m := map[string]any{"direction": in.Direction, "amount": clampInt(amount, 1, 50)}
		if in.Element != 0 {
			m["element_index"] = in.Element
		} else if len(in.Coordinate) == 2 {
			m["x"], m["y"] = in.Coordinate[0], in.Coordinate[1]
		}
		return "scroll", withCtx(m), nil
	case "type":
		return "type_text", withCtx(map[string]any{"text": in.Text}), nil
	case "key":
		parts := strings.Split(in.Keys, "+")
		var mods []string
		key := ""
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if modifierKeys[strings.ToLower(p)] {
				mods = append(mods, p)
			} else if p != "" {
				key = p
			}
		}
		if len(mods) > 0 {
			return "hotkey", withCtx(map[string]any{"keys": append(mods, key)}), nil
		}
		return "press_key", withCtx(map[string]any{"key": key}), nil
	case "set_value":
		if in.Element == 0 {
			return "", nil, fmt.Errorf("set_value requires an element")
		}
		return "set_value", withCtx(map[string]any{"element_index": in.Element, "value": in.Value}), nil
	case "list_apps":
		return "list_apps", withCtx(map[string]any{}), nil
	default:
		return "", nil, fmt.Errorf("action %q is not a direct cua-driver call", in.Action)
	}
}
