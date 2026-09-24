package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderRepoMap_BasicFormat(t *testing.T) {
	dir := t.TempDir()

	// Two Go files with a couple of definitions each.
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(`package main

type Foo struct{}

func Bar() {}
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte(`package main

type Baz struct{}

func Quux() {}
`), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := renderRepoMap(dir, 3000)
	if err != nil {
		t.Fatalf("renderRepoMap failed: %v", err)
	}

	// Each file path should appear, along with the Section: Name (line N) lines.
	for _, want := range []string{
		"Foo",
		"Bar",
		"Baz",
		"Quux",
		"(line ",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in output:\n%s", want, out)
		}
	}

	// Lines should be indented with two spaces, like "  Function: Bar (line 5)".
	lines := strings.Split(out, "\n")
	var foundIndented bool
	for _, l := range lines {
		if strings.HasPrefix(l, "  ") && strings.Contains(l, "(line ") {
			foundIndented = true
			break
		}
	}
	if !foundIndented {
		t.Fatalf("expected an indented definition line in output:\n%s", out)
	}
}

func TestRenderRepoMap_SkipDirs(t *testing.T) {
	dir := t.TempDir()

	// A real source file at the top level.
	if err := os.WriteFile(filepath.Join(dir, "real.go"), []byte(`package main
func Real() {}
`), 0644); err != nil {
		t.Fatal(err)
	}

	// A .go file inside node_modules that must be skipped.
	nodeDir := filepath.Join(dir, "node_modules", "pkg")
	if err := os.MkdirAll(nodeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeDir, "hidden.go"), []byte(`package main
func Hidden() {}
`), 0644); err != nil {
		t.Fatal(err)
	}

	// A .go file inside .git that must be skipped.
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config.go"), []byte(`package main
func Skipped() {}
`), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := renderRepoMap(dir, 3000)
	if err != nil {
		t.Fatalf("renderRepoMap failed: %v", err)
	}

	if !strings.Contains(out, "Real") {
		t.Fatalf("expected Real in output:\n%s", out)
	}
	if strings.Contains(out, "Hidden") {
		t.Fatalf("node_modules file should be skipped:\n%s", out)
	}
	if strings.Contains(out, "Skipped") {
		t.Fatalf(".git file should be skipped:\n%s", out)
	}
}

func TestRepoMapToolDefinition(t *testing.T) {
	def := repoMapToolDefinition("/work", func(p string) string { return p })
	name := def.Tool().Function.Name
	if name != "repo_map" {
		t.Fatalf("expected tool name \"repo_map\", got %q", name)
	}
}
