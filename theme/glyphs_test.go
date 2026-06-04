package theme

import "testing"

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
		return []string{PromptGlyph, ApprovalGutter, SubAgent, Arrow, ShellJob, ScrollKeys}
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
