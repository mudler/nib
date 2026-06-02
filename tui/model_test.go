package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

func TestWrapTextMultibyteStaysValid(t *testing.T) {
	long := strings.Repeat("é", 40) // one 2-byte rune, repeated, no spaces
	out := wrapText(long, 10)
	if !utf8.ValidString(out) {
		t.Fatalf("wrapText produced invalid UTF-8: %q", out)
	}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if lipgloss.Width(line) > 10 {
			t.Errorf("line %q exceeds width 10 (got %d)", line, lipgloss.Width(line))
		}
	}
}
