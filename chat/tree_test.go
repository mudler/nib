package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderTree_BasicFormat(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package x\n"), 0644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte("package x\n"), 0644)
	out, err := renderTree(dir, 2, 12)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a.go") || !strings.Contains(out, "b.go") {
		t.Fatalf("expected a.go and b.go in:\n%s", out)
	}
}

func TestRenderTree_SymbolAnnotation(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\ntype Config struct {\n\tName string\n}\n\nfunc NewConfig() *Config {\n\treturn &Config{}\n}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# readme\n"), 0644)
	out, err := renderTree(dir, 2, 12)
	if err != nil {
		t.Fatal(err)
	}
	// Source file should have bracketed symbol annotations
	if !strings.Contains(out, "[") || !strings.Contains(out, "Config") || !strings.Contains(out, "NewConfig") {
		t.Fatalf("expected symbol annotations for main.go in:\n%s", out)
	}
	// Non-source file should NOT have brackets
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.Contains(line, "readme.md") && strings.Contains(line, "[") {
			t.Fatalf("readme.md should not have symbol annotations: %s", line)
		}
	}
}

func TestRenderTree_Truncation(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 15; i++ {
		os.WriteFile(filepath.Join(dir, "file"+string(rune('a'+i))+".go"), []byte("x\n"), 0644)
	}
	out, err := renderTree(dir, 1, 12)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "more") {
		t.Fatalf("expected truncation marker in:\n%s", out)
	}
}

func TestRenderTree_SkipDirs(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".git"), 0755)
	os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("x\n"), 0644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package x\n"), 0644)
	out, err := renderTree(dir, 2, 12)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, ".git") {
		t.Fatalf(".git should be hidden:\n%s", out)
	}
	if !strings.Contains(out, "main.go") {
		t.Fatalf("main.go should appear:\n%s", out)
	}
}

func TestRenderTree_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "empty")
	os.MkdirAll(sub, 0755)
	out, err := renderTree(dir, 2, 12)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(empty directory)") {
		t.Fatalf("expected empty dir marker in:\n%s", out)
	}
}

func TestTreeToolDefinition(t *testing.T) {
	td := treeToolDefinition(func(p string) string { return p }).Tool()
	if td.Function.Name != "tree" {
		t.Fatalf("name = %q, want tree", td.Function.Name)
	}
}
