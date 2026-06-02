package tui

import (
	"strings"
	"testing"

	"github.com/mudler/nib/theme"
)

func TestRenderEmptyStateTeaches(t *testing.T) {
	out := renderEmptyState(60)
	if !strings.Contains(out, theme.EmptyTagline) {
		t.Errorf("empty state missing tagline; got:\n%s", out)
	}
	if !strings.Contains(out, theme.EmptySlash) {
		t.Errorf("empty state missing the /-discovery line; got:\n%s", out)
	}
	for _, ex := range theme.EmptyExamples {
		if !strings.Contains(out, ex) {
			t.Errorf("empty state missing example %q", ex)
		}
	}
}
