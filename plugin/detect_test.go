package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectFormat(t *testing.T) {
	native := t.TempDir()
	writeFile(t, filepath.Join(native, "nib-plugin.yaml"), "name: x")
	if got := DetectFormat(native); got != FormatNative {
		t.Fatalf("native detect = %v", got)
	}

	claude := t.TempDir()
	writeFile(t, filepath.Join(claude, ".claude-plugin", "plugin.json"), `{"name":"x"}`)
	if got := DetectFormat(claude); got != FormatClaude {
		t.Fatalf("claude detect = %v", got)
	}

	empty := t.TempDir()
	if got := DetectFormat(empty); got != FormatUnknown {
		t.Fatalf("unknown detect = %v", got)
	}
}

func TestLoadManifestNative(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "nib-plugin.yaml"), "name: demo\nversion: 1.0.0\n")
	m, err := LoadManifest(dir, "0.9.0")
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Name != "demo" || m.root != dir {
		t.Fatalf("loaded manifest wrong: %+v (root=%q)", m, m.root)
	}
}

func TestLoadManifestClaude(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"x","version":"1.0.0"}`)
	m, err := LoadManifest(dir, "0.9.0")
	if err != nil {
		t.Fatalf("expected claude plugin to load, got %v", err)
	}
	if m.Name != "x" || m.root != dir {
		t.Fatalf("claude manifest wrong: %+v (root=%q)", m, m.root)
	}
}

func TestLoadManifestUnknown(t *testing.T) {
	if _, err := LoadManifest(t.TempDir(), "0.9.0"); err == nil {
		t.Fatal("expected error for directory with no manifest")
	}
}
