package mcp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

func TestCompressOutputStripsANSI(t *testing.T) {
	in := "\x1b[32mgreen\x1b[0m \x1b[1;31mred bold\x1b[0m"
	got := compressOutput(in)
	want := "green red bold"
	if got != want {
		t.Fatalf("compressOutput(%q) = %q, want %q", in, got, want)
	}
}

func TestCompressOutputStripsCursorAndOSC(t *testing.T) {
	in := "\x1b[2J\x1b[H\x1b]0;title\x07clean"
	got := compressOutput(in)
	want := "clean"
	if got != want {
		t.Fatalf("compressOutput(%q) = %q, want %q", in, got, want)
	}
}

func TestCollapseRepeats(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "no repeats",
			in:   "a\nb\nc",
			want: "a\nb\nc",
		},
		{
			name: "two identical left alone",
			in:   "a\na\nb",
			want: "a\na\nb",
		},
		{
			name: "three collapsed",
			in:   "a\na\na\nb",
			want: "a\n  [2 repeated lines]\nb",
		},
		{
			name: "long run collapsed",
			in:   "x\nx\nx\nx\nx\nx\ny",
			want: "x\n  [5 repeated lines]\ny",
		},
		{
			name: "multiple runs",
			in:   "a\na\na\nb\nb\nb\nb\nc",
			want: "a\n  [2 repeated lines]\nb\n  [3 repeated lines]\nc",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := collapseRepeats(tc.in)
			if got != tc.want {
				t.Fatalf("collapseRepeats(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCompressOutputHeadTailBudget(t *testing.T) {
	// Unique lines so collapseRepeats doesn't shrink the output below budget.
	var lines []string
	for i := 0; i < defaultBudget*3/20; i++ {
		lines = append(lines, fmt.Sprintf("line-%05d %s", i, strings.Repeat("x", 10)))
	}
	big := strings.Join(lines, "\n")
	got := compressOutput(big)

	// Must start with the warning
	if !strings.HasPrefix(got, "⚠ Output was") {
		t.Fatalf("missing truncation warning, got: %q", got[:min(80, len(got))])
	}

	// Must contain an elision marker
	if !strings.Contains(got, "bytes elided") {
		t.Fatal("missing elision marker")
	}

	// Must contain the head/tail separator
	if !strings.Contains(got, "… … …") {
		t.Fatal("missing head/tail separator")
	}
}

func TestCompressOutputUnderBudgetUntouched(t *testing.T) {
	small := "hello world"
	got := compressOutput(small)
	if got != small {
		t.Fatalf("compressOutput(%q) = %q, want %q", small, got, small)
	}
}

func TestCompressOutputExactlyAtBudgetUntouched(t *testing.T) {
	// Build input of exactly defaultBudget bytes, with unique short lines
	// so neither collapseRepeats nor truncateLongLines shrinks it.
	var lines []string
	n := 0
	for n < defaultBudget {
		rem := defaultBudget - n
		line := fmt.Sprintf("line-%05d", n/20)
		if len(line)+1 > rem {
			line = line[:rem]
			n += len(line)
		} else {
			n += len(line) + 1 // +1 for \n
		}
		lines = append(lines, line)
	}
	exact := strings.Join(lines, "\n")
	if len(exact) != defaultBudget {
		// pad to exact
		if len(exact) < defaultBudget {
			exact += strings.Repeat("a", defaultBudget-len(exact))
		} else {
			exact = exact[:defaultBudget]
		}
	}
	if len(exact) != defaultBudget {
		t.Fatalf("setup: len(exact)=%d, want %d", len(exact), defaultBudget)
	}
	got := compressOutput(exact)
	if len(got) != len(exact) {
		t.Fatalf("output at exactly budget should be untouched (len in=%d, out=%d)", len(exact), len(got))
	}
}

func TestCompressOutputANSIThenCollapseOrder(t *testing.T) {
	// ANSI codes stripped first, so lines that differ only by colour collapse.
	in := "\x1b[32mfoo\x1b[0m\n\x1b[32mfoo\x1b[0m\n\x1b[32mfoo\x1b[0m\nbar"
	got := compressOutput(in)
	want := "foo\n  [2 repeated lines]\nbar"
	if got != want {
		t.Fatalf("compressOutput = %q, want %q", got, want)
	}
}

func TestCompressOutputHeadAndTailContent(t *testing.T) {
	// With a head and a tail separated by unique-line filler, we should see both.
	// Lines must differ so collapseRepeats doesn't shrink the output below budget.
	head := "HEAD_START\n"
	tail := "\nTAIL_END"
	var lines []string
	for i := 0; i < defaultBudget*2/20; i++ {
		lines = append(lines, fmt.Sprintf("line %d: %s", i, strings.Repeat("z", 10)))
	}
	middle := strings.Join(lines, "\n") + "\n"
	big := head + middle + tail

	got := compressOutput(big)

	if !strings.Contains(got, "⚠") {
		t.Fatal("missing warning")
	}
	if !strings.Contains(got, "HEAD_START") {
		t.Fatal("head content not preserved")
	}
	if !strings.Contains(got, "TAIL_END") {
		t.Fatal("tail content not preserved")
	}
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1 KB"},
		{1536, "1 KB"},
		{16 * 1024, "16 KB"},
		{1024 * 1024, "1 MB"},
	}
	for _, tc := range tests {
		got := humanBytes(tc.in)
		if got != tc.want {
			t.Fatalf("humanBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// --- LimitOutput tests ---

func TestLimitOutputUnderBudget(t *testing.T) {
	limits := OutputLimits{Budget: 1024, HeadBudget: 256}
	got := LimitOutput("hello", "bash", limits, nil)
	if got != "hello" {
		t.Fatalf("output under budget should be untouched, got %q", got)
	}
}

func TestLimitOutputDisabled(t *testing.T) {
	limits := OutputLimits{Disabled: true, Budget: 1}
	big := strings.Repeat("x", 10000)
	got := LimitOutput(big, "bash", limits, nil)
	if got != big {
		t.Fatal("disabled limits should leave output untouched")
	}
}

func TestLimitOutputTruncatesLongLines(t *testing.T) {
	limits := OutputLimits{Budget: 10000, MaxLineLength: 10}
	longLine := strings.Repeat("a", 50)
	got := LimitOutput(longLine, "bash", limits, nil)
	// The line should be truncated to 10 runes + " …"
	if !strings.HasSuffix(got, " …") {
		t.Fatalf("truncated line should end with ' …', got %q", got)
	}
	runes := []rune(strings.TrimSuffix(got, " …"))
	if len(runes) != 10 {
		t.Fatalf("expected 10 runes, got %d", len(runes))
	}
}

func TestLimitOutputPerLineTruncationMultipleLines(t *testing.T) {
	limits := OutputLimits{Budget: 10000, MaxLineLength: 5}
	in := strings.Repeat("a", 20) + "\n" + strings.Repeat("b", 20) + "\nshort"
	got := LimitOutput(in, "bash", limits, nil)
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	for i, line := range lines {
		runes := []rune(line)
		expected := 5
		if line == "short" {
			expected = 5 // "short" is exactly 5 chars
		}
		// Truncated lines have " …" suffix (2 runes), so total is maxLen + 2
		if strings.HasSuffix(line, " …") {
			if len(runes) != expected+2 {
				t.Fatalf("line %d: expected %d runes (including suffix), got %d", i, expected+2, len(runes))
			}
		} else {
			if len(runes) > expected {
				t.Fatalf("line %d: expected at most %d runes, got %d", i, expected, len(runes))
			}
		}
	}
}

func TestLimitOutputArtifactSpill(t *testing.T) {
	store := NewArtifactStore()
	limits := OutputLimits{
		Budget:                100,
		HeadBudget:            20,
		ArtifactSpillThreshold: 50,
	}
	big := strings.Repeat("x", 500)
	got := LimitOutput(big, "bash", limits, store)

	if !strings.Contains(got, "📦") {
		t.Fatal("expected artifact spill notice")
	}
	if !strings.Contains(got, "artifact://") {
		t.Fatal("expected artifact:// URI in output")
	}
	if store.Count() != 1 {
		t.Fatalf("expected 1 artifact in store, got %d", store.Count())
	}
}

func TestLimitOutputNoArtifactUnderThreshold(t *testing.T) {
	store := NewArtifactStore()
	limits := OutputLimits{
		Budget:                100,
		HeadBudget:            20,
		ArtifactSpillThreshold: 1000, // higher than the output
	}
	big := strings.Repeat("x", 200) // over budget but under spill threshold
	got := LimitOutput(big, "bash", limits, store)

	if strings.Contains(got, "📦") {
		t.Fatal("should not spill when under threshold")
	}
	if store.Count() != 0 {
		t.Fatalf("expected 0 artifacts, got %d", store.Count())
	}
}

func TestLimitOutputNoSpillWithoutStore(t *testing.T) {
	limits := OutputLimits{
		Budget:                100,
		HeadBudget:            20,
		ArtifactSpillThreshold: 50, // would spill, but store is nil
	}
	big := strings.Repeat("x", 500)
	got := LimitOutput(big, "bash", limits, nil)

	if strings.Contains(got, "📦") {
		t.Fatal("should not spill without a store")
	}
}

// --- ArtifactStore tests ---

func TestArtifactStoreSaveAndGet(t *testing.T) {
	store := NewArtifactStore()
	uri := store.Save("bash", "hello world")

	id, ok := ParseArtifactURI(uri)
	if !ok {
		t.Fatalf("could not parse URI %q", uri)
	}
	a := store.Get(id)
	if a == nil {
		t.Fatal("artifact not found")
	}
	if a.Content != "hello world" {
		t.Fatalf("expected 'hello world', got %q", a.Content)
	}
	if a.Tool != "bash" {
		t.Fatalf("expected tool 'bash', got %q", a.Tool)
	}
}

func TestArtifactStoreGetMissing(t *testing.T) {
	store := NewArtifactStore()
	if a := store.Get(999); a != nil {
		t.Fatal("expected nil for missing artifact")
	}
}

func TestArtifactStoreMultipleSaves(t *testing.T) {
	store := NewArtifactStore()
	uri1 := store.Save("bash", "first")
	uri2 := store.Save("read", "second")

	id1, _ := ParseArtifactURI(uri1)
	id2, _ := ParseArtifactURI(uri2)

	if id1 == id2 {
		t.Fatal("expected different IDs")
	}
	if store.Count() != 2 {
		t.Fatalf("expected 2 artifacts, got %d", store.Count())
	}
}

// --- ParseArtifactURI tests ---

func TestParseArtifactURI(t *testing.T) {
	tests := []struct {
		in   string
		id   int64
		ok   bool
	}{
		{"artifact://1", 1, true},
		{"artifact://42", 42, true},
		{"artifact://0", 0, false}, // 0 is not a valid artifact ID
		{"artifact://", 0, false},
		{"artifact://abc", 0, false},
		{"not-an-artifact", 0, false},
		{"", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			id, ok := ParseArtifactURI(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if id != tc.id {
				t.Fatalf("id = %d, want %d", id, tc.id)
			}
		})
	}
}

// --- ResolveOutputLimits tests ---

func TestResolveOutputLimitsDefaults(t *testing.T) {
	resolved := ResolveOutputLimits(types.ToolOutputLimitsConfig{})
	if resolved.Budget != defaultBudget {
		t.Fatalf("Budget = %d, want %d", resolved.Budget, defaultBudget)
	}
	if resolved.HeadBudget != defaultHeadBudget {
		t.Fatalf("HeadBudget = %d, want %d", resolved.HeadBudget, defaultHeadBudget)
	}
	if resolved.MaxLineLength != defaultMaxLineLen {
		t.Fatalf("MaxLineLength = %d, want %d", resolved.MaxLineLength, defaultMaxLineLen)
	}
	if resolved.ArtifactSpillThreshold != defaultSpillThreshold {
		t.Fatalf("ArtifactSpillThreshold = %d, want %d", resolved.ArtifactSpillThreshold, defaultSpillThreshold)
	}
}

func TestResolveOutputLimitsExplicitValues(t *testing.T) {
	cfg := types.ToolOutputLimitsConfig{
		Budget:                8000,
		HeadBudget:            2000,
		MaxLineLength:         500,
		ArtifactSpillThreshold: 32000,
	}
	resolved := ResolveOutputLimits(cfg)
	if resolved.Budget != 8000 {
		t.Fatalf("Budget = %d, want 8000", resolved.Budget)
	}
	if resolved.HeadBudget != 2000 {
		t.Fatalf("HeadBudget = %d, want 2000", resolved.HeadBudget)
	}
	if resolved.MaxLineLength != 500 {
		t.Fatalf("MaxLineLength = %d, want 500", resolved.MaxLineLength)
	}
	if resolved.ArtifactSpillThreshold != 32000 {
		t.Fatalf("ArtifactSpillThreshold = %d, want 32000", resolved.ArtifactSpillThreshold)
	}
}

func TestResolveOutputLimitsHeadBudgetClamped(t *testing.T) {
	cfg := types.ToolOutputLimitsConfig{
		Budget:     1000,
		HeadBudget: 2000, // larger than budget
	}
	resolved := ResolveOutputLimits(cfg)
	if resolved.HeadBudget >= resolved.Budget {
		t.Fatalf("HeadBudget (%d) should be < Budget (%d)", resolved.HeadBudget, resolved.Budget)
	}
}

// --- OutputLimitsPolicy tests ---

func TestOutputLimitsPolicyGetSet(t *testing.T) {
	cfg := types.ToolOutputLimitsConfig{Budget: 5000, HeadBudget: 1000}
	policy := NewOutputLimitsPolicy(cfg)

	got := policy.Get()
	if got.Budget != 5000 {
		t.Fatalf("Budget = %d, want 5000", got.Budget)
	}

	policy.Set(types.ToolOutputLimitsConfig{Budget: 9000})
	got = policy.Get()
	if got.Budget != 9000 {
		t.Fatalf("after Set, Budget = %d, want 9000", got.Budget)
	}
}

func TestOutputLimitsPolicyResolved(t *testing.T) {
	policy := NewOutputLimitsPolicy(types.ToolOutputLimitsConfig{})
	resolved := policy.Resolved()
	if resolved.Budget != defaultBudget {
		t.Fatalf("resolved Budget = %d, want %d", resolved.Budget, defaultBudget)
	}
}
