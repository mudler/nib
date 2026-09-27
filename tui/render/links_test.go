package render

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestFitWidthKeepsAllText(t *testing.T) {
	long := strings.Repeat("abcdefghij", 25)
	in := "short line\n" + long + "\nend"
	out := FitWidth(in, 40)
	for i, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w > 40 {
			t.Fatalf("line %d is %d cells, over 40", i, w)
		}
	}
	if got := strings.ReplaceAll(out, "\n", ""); got != strings.ReplaceAll(in, "\n", "") {
		t.Fatalf("FitWidth changed the text:\n%q\nwant\n%q", got, in)
	}
}

func TestWrapLinkedSplitsLongURLIntoLinkedPieces(t *testing.T) {
	url := "https://example.com/" + strings.Repeat("x", 90)
	out := WrapLinked("Open:\n  "+url+"\nthen paste", 40)
	lines := strings.Split(out, "\n")
	if ansi.Strip(lines[0]) != "Open:" || ansi.Strip(lines[len(lines)-1]) != "then paste" {
		t.Fatalf("line breaks not kept: %q", out)
	}
	var joined strings.Builder
	for _, l := range lines[1 : len(lines)-1] {
		if w := ansi.StringWidth(l); w > 40 {
			t.Fatalf("line %q is %d cells, over 40", ansi.Strip(l), w)
		}
		if !strings.Contains(l, ";"+url) {
			t.Fatalf("URL piece %q does not link to the whole URL", ansi.Strip(l))
		}
		joined.WriteString(strings.TrimSpace(ansi.Strip(l)))
	}
	if joined.String() != url {
		t.Fatalf("pieces join to %q, want %q", joined.String(), url)
	}
}

func TestWrapLinkedKeepsIndentAndPlaceholders(t *testing.T) {
	out := ansi.Strip(WrapLinked("  ssh -L 1:127.0.0.1:1 <user>@<this-host>", 80))
	if out != "  ssh -L 1:127.0.0.1:1 <user>@<this-host>" {
		t.Fatalf("got %q", out)
	}
}

func TestWrapLinkedWordWrapsProse(t *testing.T) {
	out := WrapLinked("one two three four five six seven", 10)
	for _, l := range strings.Split(out, "\n") {
		if ansi.StringWidth(l) > 10 {
			t.Fatalf("line %q over 10 cells", l)
		}
	}
	if got := strings.Join(strings.Fields(strings.ReplaceAll(out, "\n", " ")), " "); got != "one two three four five six seven" {
		t.Fatalf("words changed: %q", got)
	}
}
