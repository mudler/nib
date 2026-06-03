package chat

import (
	"strings"
	"testing"
)

func TestPrettyJSON(t *testing.T) {
	out := PrettyJSON(`{"a":1,"b":[2,3]}`)
	if !strings.Contains(out, "\n  ") {
		t.Fatalf("expected indented multi-line output, got %q", out)
	}

	const invalid = "not json"
	if got := PrettyJSON(invalid); got != invalid {
		t.Fatalf("expected invalid input returned unchanged, got %q", got)
	}
}

func TestPreviewResult(t *testing.T) {
	t.Run("pretty-prints and truncates long JSON", func(t *testing.T) {
		// Build a JSON object that pretty-prints to 22 lines: opening brace,
		// 20 fields, closing brace.
		var b strings.Builder
		b.WriteString("{")
		for i := 0; i < 20; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`"k`)
			b.WriteString(string(rune('A' + i)))
			b.WriteString(`":`)
			b.WriteString("1")
		}
		b.WriteString("}")

		out := PreviewResult(b.String(), 5)
		lines := strings.Split(out, "\n")
		if len(lines) != 6 {
			t.Fatalf("expected 6 lines (5 + note), got %d:\n%s", len(lines), out)
		}
		// Pretty-printed: line 0 is "{", lines 1-4 are fields, indented.
		if !strings.HasPrefix(lines[1], "  ") {
			t.Fatalf("expected pretty-printed (indented) output, got %q", lines[1])
		}
		note := lines[len(lines)-1]
		// 22 total lines, 5 kept -> 17 cut.
		if note != "… 17 more lines" {
			t.Fatalf("unexpected truncation note: %q", note)
		}
	})

	t.Run("short plain string returned unchanged", func(t *testing.T) {
		in := "line one\nline two"
		if got := PreviewResult(in, 12); got != in {
			t.Fatalf("expected %q unchanged, got %q", in, got)
		}
	})

	t.Run("single line truncation uses singular noun", func(t *testing.T) {
		in := "a\nb\nc"
		out := PreviewResult(in, 2)
		lines := strings.Split(out, "\n")
		if len(lines) != 3 {
			t.Fatalf("expected 3 lines, got %d: %q", len(lines), out)
		}
		if lines[len(lines)-1] != "… 1 more line" {
			t.Fatalf("expected singular 'line' note, got %q", lines[len(lines)-1])
		}
	})

	t.Run("empty and whitespace return empty", func(t *testing.T) {
		if got := PreviewResult("", 12); got != "" {
			t.Fatalf("expected empty, got %q", got)
		}
		if got := PreviewResult("   \n\t  ", 12); got != "" {
			t.Fatalf("expected empty for whitespace, got %q", got)
		}
	})
}
