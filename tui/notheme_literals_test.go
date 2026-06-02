package tui

import (
	"os"
	"strings"
	"testing"
)

// TestNoRawColorLiteralsInTUI enforces that the theme package is the single
// source of truth for color: no non-test file in the tui package may use a raw
// lipgloss.Color( literal. All inks must come from theme.
func TestNoRawColorLiteralsInTUI(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "lipgloss.Color(") {
			t.Errorf("%s contains a raw lipgloss.Color( literal; colors must come from the theme package", name)
		}
	}
}
