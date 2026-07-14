// ~/_git/wiz/mcp/computer_map_test.go
package mcp

import "testing"

func TestMapClickByElement(t *testing.T) {
	tool, args, err := buildCuaCall(
		ComputerUseInput{Action: "click", Element: 14, Button: "left"},
		StickyContext{PID: 42, WindowID: 7, SessionID: "dante-x"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if tool != "click" {
		t.Fatalf("tool=%q", tool)
	}
	if args["element_index"] != 14 || args["pid"] != 42 || args["session"] != "dante-x" {
		t.Fatalf("args=%+v", args)
	}
}

func TestMapKeySplitsHotkeyVsPress(t *testing.T) {
	tool, _, _ := buildCuaCall(ComputerUseInput{Action: "key", Keys: "cmd+s"}, StickyContext{PID: 1})
	if tool != "hotkey" {
		t.Fatalf("modified combo should be hotkey, got %q", tool)
	}
	tool2, args2, _ := buildCuaCall(ComputerUseInput{Action: "key", Keys: "return"}, StickyContext{PID: 1})
	if tool2 != "press_key" || args2["key"] != "return" {
		t.Fatalf("bare key should be press_key{key}, got %q %+v", tool2, args2)
	}
}

// Regression: qwen emitted `keys` as a JSON-array *string* (["return"]) and used
// "enter" (not a driver key name). Both must be normalized, or press_key fails
// with "Unknown key name" and the model loops forever pressing it.
func TestMapKeyNormalizesArrayAndAliases(t *testing.T) {
	cases := []struct {
		keys     string
		wantTool string
		wantKey  string // press_key key, or the last element for hotkey
	}{
		{`["return"]`, "press_key", "return"},   // JSON-array string, single key
		{`["enter"]`, "press_key", "return"},    // array string + alias
		{"enter", "press_key", "return"},        // bare alias
		{"ESC", "press_key", "escape"},          // upper-case alias
		{`["ctrl","c"]`, "hotkey", "c"},         // JSON-array combo
		{"cmd+return", "hotkey", "return"},      // combo with an aliasable name
	}
	for _, c := range cases {
		tool, args, err := buildCuaCall(ComputerUseInput{Action: "key", Keys: c.keys}, StickyContext{PID: 1})
		if err != nil {
			t.Fatalf("keys=%q: %v", c.keys, err)
		}
		if tool != c.wantTool {
			t.Fatalf("keys=%q: tool=%q want %q", c.keys, tool, c.wantTool)
		}
		if tool == "press_key" {
			if args["key"] != c.wantKey {
				t.Fatalf("keys=%q: press_key key=%v want %q", c.keys, args["key"], c.wantKey)
			}
		} else {
			combo := args["keys"].([]string)
			if combo[len(combo)-1] != c.wantKey {
				t.Fatalf("keys=%q: hotkey combo=%v want last %q", c.keys, combo, c.wantKey)
			}
		}
	}
}

func TestMapKeyEmptyErrors(t *testing.T) {
	if _, _, err := buildCuaCall(ComputerUseInput{Action: "key", Keys: "[]"}, StickyContext{PID: 1}); err == nil {
		t.Fatalf("empty key spec must error, not send an empty press_key")
	}
}

func TestMapMutatingRequiresContext(t *testing.T) {
	if _, _, err := buildCuaCall(ComputerUseInput{Action: "click", Element: 1}, StickyContext{}); err == nil {
		t.Fatalf("click without prior capture (no pid) must error")
	}
}

func TestMapTypeText(t *testing.T) {
	tool, args, _ := buildCuaCall(ComputerUseInput{Action: "type", Text: "hi"}, StickyContext{PID: 5})
	if tool != "type_text" || args["text"] != "hi" || args["pid"] != 5 {
		t.Fatalf("tool=%q args=%+v", tool, args)
	}
}
