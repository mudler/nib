package tui

import (
	"strings"
	"testing"

	"github.com/mudler/nib/chat"
)

func TestRenderAskAndParse(t *testing.T) {
	req := chat.AskRequest{Question: "Pick one", Options: []string{"alpha", "beta"}}
	out := renderAsk(req, 80)
	if !strings.Contains(out, "Pick one") || !strings.Contains(out, "1.") || !strings.Contains(out, "alpha") {
		t.Fatalf("ask render missing question/options:\n%s", out)
	}
	if got := parseAskAnswer("2", req); got != "beta" {
		t.Fatalf("numeric pick: %q", got)
	}
	if got := parseAskAnswer("something else", req); got != "something else" {
		t.Fatalf("free text: %q", got)
	}
	if got := parseAskAnswer("9", req); got != "9" {
		t.Fatalf("out-of-range should be verbatim: %q", got)
	}
	if got := parseAskAnswer("hi", chat.AskRequest{Question: "q"}); got != "hi" {
		t.Fatalf("no-options verbatim: %q", got)
	}
	// Single-select shows a radio marker.
	if !strings.Contains(out, "( )") {
		t.Fatalf("single-select should show radio marker:\n%s", out)
	}
}

func TestAskMultiSelect(t *testing.T) {
	req := chat.AskRequest{
		Question:    "Pick some",
		Options:     []string{"red", "green", "blue"},
		MultiSelect: true,
	}
	out := renderAsk(req, 80)
	if !strings.Contains(out, "[ ]") || !strings.Contains(out, "commas") {
		t.Fatalf("multi-select render missing checkbox/hint:\n%s", out)
	}
	if got := parseAskAnswer("1,3", req); got != "red, blue" {
		t.Fatalf("comma indices: %q", got)
	}
	if got := parseAskAnswer("2 3", req); got != "green, blue" {
		t.Fatalf("space indices: %q", got)
	}
	if got := parseAskAnswer("2", req); got != "green" {
		t.Fatalf("single index in multi: %q", got)
	}
	if got := parseAskAnswer("1,9", req); got != "1,9" {
		t.Fatalf("invalid index should be verbatim: %q", got)
	}
	if got := parseAskAnswer("my own answer", req); got != "my own answer" {
		t.Fatalf("free text in multi: %q", got)
	}
}
