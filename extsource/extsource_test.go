package extsource

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// writeZip builds a zip at path from name→content entries (dirs end in "/").
func writeZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	for name, body := range entries {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

// writeZipWithSymlink builds a zip at path containing a single symlink entry
// (name → link target text). w.Create cannot set a symlink mode, so this uses
// CreateHeader with a header whose mode marks the entry a symlink.
func writeZipWithSymlink(t *testing.T, path, name, linkTarget string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	hdr := &zip.FileHeader{Name: name}
	hdr.SetMode(os.ModeSymlink | 0o777)
	fw, err := w.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(linkTarget)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExtractZip_OK(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "pack.zip")
	writeZip(t, zp, map[string]string{
		"SKILL.md":       "---\nname: x\ndescription: y\n---\nbody",
		"scripts/run.sh": "#!/bin/sh\necho hi",
	})
	dest := filepath.Join(dir, "out")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ExtractZip(zp, dest); err != nil {
		t.Fatalf("ExtractZip: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "SKILL.md")); err != nil || string(b) == "" {
		t.Fatalf("SKILL.md not extracted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "scripts", "run.sh")); err != nil {
		t.Fatalf("nested file not extracted: %v", err)
	}
}

func TestExtractZip_SlipRejected(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "evil.zip")
	writeZip(t, zp, map[string]string{"../escape.txt": "pwned"})
	dest := filepath.Join(dir, "out")
	os.MkdirAll(dest, 0o755)
	if err := ExtractZip(zp, dest); err == nil {
		t.Fatal("expected zip-slip entry to be rejected")
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.txt")); err == nil {
		t.Fatal("escape file was written outside destDir")
	}
}

func TestExtractZip_AbsolutePathRejected(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "abs.zip")
	writeZip(t, zp, map[string]string{"/etc/passwd": "pwned"})
	dest := filepath.Join(dir, "out")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ExtractZip(zp, dest); err == nil {
		t.Fatal("expected absolute-path entry to be rejected")
	}
}

func TestExtractZip_SymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "symlink.zip")
	writeZipWithSymlink(t, zp, "link", "/etc/passwd")
	dest := filepath.Join(dir, "out")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ExtractZip(zp, dest); err == nil {
		t.Fatal("expected symlink entry to be rejected")
	}
	if _, err := os.Lstat(filepath.Join(dest, "link")); err == nil {
		t.Fatal("symlink was materialized in destDir")
	}
}
