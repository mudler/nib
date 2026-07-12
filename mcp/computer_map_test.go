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
