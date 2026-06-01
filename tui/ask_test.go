package tui

import (
	"strings"
	"testing"

	"github.com/mudler/wiz/chat"
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
}
