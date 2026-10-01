package theme

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// isASCII reports whether every rune in s is in the 7-bit ASCII range — i.e.
// the glyph is guaranteed to render on a fixed bitmap VT-console font.
func isASCII(s string) bool {
	for _, r := range s {
		if r > 0x7F {
			return false
		}
	}
	return true
}

func TestRestrictedGlyphs(t *testing.T) {
	cases := []struct {
		name     string
		nibASCII string
		term     string
		setASCII bool
		setTerm  bool
		want     bool
	}{
		{name: "default xterm", term: "xterm-256color", setTerm: true, want: false},
		{name: "vt console", term: "linux", setTerm: true, want: true},
		{name: "force on", nibASCII: "1", setASCII: true, want: true},
		{name: "force on yes", nibASCII: "yes", setASCII: true, want: true},
		{name: "force off beats TERM=linux", nibASCII: "0", term: "linux", setASCII: true, setTerm: true, want: false},
		{name: "force off no", nibASCII: "no", setASCII: true, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setASCII {
				t.Setenv("NIB_ASCII", tc.nibASCII)
			} else {
				t.Setenv("NIB_ASCII", "")
			}
			if tc.setTerm {
				t.Setenv("TERM", tc.term)
			} else {
				t.Setenv("TERM", "")
			}
			if got := RestrictedGlyphs(); got != tc.want {
				t.Fatalf("RestrictedGlyphs() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestApplyGlyphProfile verifies the restricted profile yields pure-ASCII
// stand-ins and the full profile restores the typographic glyphs.
func TestApplyGlyphProfile(t *testing.T) {
	// Restore whatever profile the ambient env implies once we're done mutating
	// the package-level glyph vars.
	t.Cleanup(applyGlyphProfile)

	swappable := func() []string {
		return []string{PromptGlyph, ApprovalGutter, MsgGutter, SubAgent, Arrow, Loop, ShellJob, Goal, ScrollKeys, ReasoningGlyph, NewOutputGlyph, HairlineGlyph, BoxRule, RadioOn, RadioOff, CheckOn, CheckOff, Cursor, ReadyMarker, WaitingMarker, ApprovalMarker, ParkedMarker, InterruptingMarker}
	}

	t.Setenv("NIB_ASCII", "1")
	applyGlyphProfile()
	for _, g := range swappable() {
		if !isASCII(g) {
			t.Fatalf("restricted glyph %q is not ASCII", g)
		}
	}

	t.Setenv("NIB_ASCII", "0")
	applyGlyphProfile()
	var anyNonASCII bool
	for _, g := range swappable() {
		if !isASCII(g) {
			anyNonASCII = true
		}
	}
	if !anyNonASCII {
		t.Fatal("full profile should restore non-ASCII typographic glyphs")
	}
}

func TestLifecycleMarkersCarryStableDistinctMeanings(t *testing.T) {
	t.Cleanup(applyGlyphProfile)
	for _, ascii := range []string{"0", "1"} {
		t.Setenv("NIB_ASCII", ascii)
		applyGlyphProfile()
		markers := []string{ReadyMarker, WaitingMarker, ApprovalMarker, ParkedMarker, InterruptingMarker}
		seen := map[string]bool{}
		for _, marker := range markers {
			if marker == "" || seen[marker] {
				t.Fatalf("NIB_ASCII=%s lifecycle markers are not distinct and nonempty: %q", ascii, markers)
			}
			seen[marker] = true
			if ascii == "1" && !isASCII(marker) {
				t.Fatalf("restricted lifecycle marker %q is not ASCII", marker)
			}
			for _, r := range marker {
				if (r >= 0x1F000 && r <= 0x1FAFF) || (r >= 0x2600 && r <= 0x27BF) {
					t.Fatalf("NIB_ASCII=%s lifecycle marker %q contains emoji rune %U", ascii, marker, r)
				}
			}
		}
	}
}

// TestHairlineRespectsGlyphProfile pins the rule that moved out of the
// presenters: the hairline is one repeated glyph, and it swaps to ASCII on a
// restricted terminal like every other non-Latin-1 mark.
func TestHairlineRespectsGlyphProfile(t *testing.T) {
	t.Cleanup(applyGlyphProfile)

	t.Setenv("NIB_ASCII", "1")
	applyGlyphProfile()
	got := Hairline(5)
	if !isASCII(got) {
		t.Errorf("Hairline(5) = %q, want ASCII on a restricted terminal", got)
	}

	t.Setenv("NIB_ASCII", "0")
	applyGlyphProfile()
	if got := lipgloss.Width(Hairline(7)); got != 7 {
		t.Errorf("Hairline(7) width = %d, want 7", got)
	}
	// A degenerate width must still produce a rule, never the empty string.
	if got := lipgloss.Width(Hairline(0)); got != 1 {
		t.Errorf("Hairline(0) width = %d, want 1", got)
	}
}

// TestBoxRuleRespectsGlyphProfile pins the collapsed reasoning box's side
// rule to the same restricted-terminal contract as every other swappable
// glyph: ASCII on the Linux VT console, the typographic mark otherwise. This
// is what TestApplyGlyphProfile's swappable() list already checks generically
// (BoxRule is included there); this test additionally pins the exact ASCII
// stand-in ("|"), the same way TestHairlineRespectsGlyphProfile does for
// HairlineGlyph, so a future edit that desyncs the restricted branch fails on
// content, not just on "is it ASCII".
func TestBoxRuleRespectsGlyphProfile(t *testing.T) {
	t.Cleanup(applyGlyphProfile)

	t.Setenv("NIB_ASCII", "1")
	applyGlyphProfile()
	if BoxRule != "|" {
		t.Errorf("BoxRule = %q on a restricted terminal, want %q", BoxRule, "|")
	}

	t.Setenv("NIB_ASCII", "0")
	applyGlyphProfile()
	if BoxRule != "│" {
		t.Errorf("BoxRule = %q on a full-glyph terminal, want %q", BoxRule, "│")
	}
}
