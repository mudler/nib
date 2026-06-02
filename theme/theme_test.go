package theme_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/mudler/nib/theme"
)

func TestColorsAreSet(t *testing.T) {
	for name, c := range map[string]lipgloss.Color{
		"Accent": theme.Accent, "Sage": theme.Sage, "Danger": theme.Danger,
		"Dim": theme.Dim, "Faint": theme.Faint,
	} {
		if string(c) == "" {
			t.Errorf("%s color is empty", name)
		}
	}
}

func TestGlyphsHaveNoEmoji(t *testing.T) {
	for _, g := range []string{theme.Sep, theme.PromptGlyph, theme.ApprovalGutter, theme.SubAgent, theme.Cross} {
		for _, r := range g {
			if r >= 0x1F000 {
				t.Errorf("glyph %q contains emoji rune %U", g, r)
			}
		}
	}
}

func TestStatusGrowsDots(t *testing.T) {
	want := map[int]string{0: "thinking", 1: "thinking.", 2: "thinking..", 3: "thinking…", 4: "thinking"}
	for phase, exp := range want {
		if got := theme.Status("thinking", phase); got != exp {
			t.Errorf("Status(%d) = %q, want %q", phase, got, exp)
		}
	}
}

func TestEmptyStateCopyPresent(t *testing.T) {
	if theme.EmptyTagline == "" {
		t.Error("EmptyTagline is empty")
	}
	if len(theme.EmptyExamples) < 1 {
		t.Error("EmptyExamples is empty")
	}
	if !strings.Contains(theme.EmptySlash, "/") {
		t.Errorf("EmptySlash %q should mention /", theme.EmptySlash)
	}
}
