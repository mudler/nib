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
