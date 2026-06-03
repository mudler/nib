package chat

import "testing"

func TestFormatToolCall_Fallback(t *testing.T) {
	// Unknown tool → humanized, sorted key: value lines.
	got := FormatToolCall("some_mcp_tool", `{"beta":"two","alpha":1}`)
	want := "alpha: 1\nbeta: two"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatToolCall_FallbackMultiline(t *testing.T) {
	// Multi-line / long string values go on their own indented block.
	got := FormatToolCall("x", `{"body":"line one\nline two"}`)
	want := "body:\n  line one\n  line two"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatToolCall_InvalidJSON(t *testing.T) {
	in := "not json at all"
	if got := FormatToolCall("x", in); got != in {
		t.Fatalf("got %q want %q", got, in)
	}
}
