package chat

import (
	"path/filepath"
	"testing"
)

func TestResolveWorkspacePath(t *testing.T) {
	if got := resolveWorkspacePath("/work", "a.png"); got != filepath.Join("/work", "a.png") {
		t.Fatalf("relative should join workingDir, got %q", got)
	}
	if got := resolveWorkspacePath("/work", "/abs/a.png"); got != "/abs/a.png" {
		t.Fatalf("absolute should be unchanged, got %q", got)
	}
	if got := resolveWorkspacePath("", "a.png"); got != "a.png" {
		t.Fatalf("empty workingDir should leave path, got %q", got)
	}
}
