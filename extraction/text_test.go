package extraction

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadTextFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "note.md")
	if err := os.WriteFile(p, []byte("# Title\n\ncafé ☕\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadTextFile(p)
	if err != nil {
		t.Fatalf("ReadTextFile: %v", err)
	}
	if got != "# Title\n\ncafé ☕\n" {
		t.Fatalf("unexpected content %q", got)
	}
}

func TestIsPlainTextFile(t *testing.T) {
	if !IsPlainTextFile("a.md") || !IsPlainTextFile("b.CSV") {
		t.Fatal("expected md/CSV to be plain text")
	}
	if IsPlainTextFile("c.pdf") {
		t.Fatal("pdf is not plain text")
	}
}
