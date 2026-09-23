package mcp

import (
	"strings"
	"testing"
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
	big := strings.Repeat("x", bashOutputBudget*3)
	got := compressOutput(big)

	// Must start with the warning
	if !strings.HasPrefix(got, "⚠ Output was") {
		t.Fatalf("missing truncation warning, got: %q", got[:min(80, len(got))])
	}

	// Must contain an elision marker
	if !strings.Contains(got, "bytes elided") {
		t.Fatal("missing elision marker")
	}

	// Must contain the head separator
	if !strings.Contains(got, "… … …") {
		t.Fatal("missing head/tail separator")
	}

	// Head: first bashHeadBudget bytes of the original
	headPart := strings.Repeat("x", bashHeadBudget)
	if !strings.Contains(got, headPart) {
		t.Fatal("head not preserved")
	}

	// Tail: last (budget - head) bytes of the original
	tailBudget := bashOutputBudget - bashHeadBudget
	tailPart := strings.Repeat("x", tailBudget)
	if !strings.HasSuffix(got, tailPart) {
		t.Fatal("tail not preserved")
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
	exact := strings.Repeat("a", bashOutputBudget)
	got := compressOutput(exact)
	if got != exact {
		t.Fatalf("output at exactly budget should be untouched")
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
	// With a head and a tail separated by filler, we should see both.
	head := "HEAD_START"
	tail := "TAIL_END"
	middle := strings.Repeat("z", bashOutputBudget*2)
	big := head + middle + tail

	got := compressOutput(big)

	if !strings.Contains(got, "⚠") {
		t.Fatal("missing warning")
	}
	if !strings.Contains(got, head) {
		t.Fatal("head content not preserved")
	}
	if !strings.Contains(got, tail) {
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
