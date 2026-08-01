package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/nib/types"
)

func TestWithDefaultsCompaction(t *testing.T) {
	cfg := withDefaults(types.Config{})
	if cfg.Compaction.MaxContextTokens != 128000 {
		t.Fatalf("MaxContextTokens default = %d, want 128000", cfg.Compaction.MaxContextTokens)
	}
	if cfg.Compaction.Threshold != 0.8 {
		t.Fatalf("Threshold default = %v, want 0.8", cfg.Compaction.Threshold)
	}
	if cfg.Compaction.KeepRecent != 8 {
		t.Fatalf("KeepRecent default = %d, want 8", cfg.Compaction.KeepRecent)
	}
	if cfg.Compaction.Disabled {
		t.Fatal("auto-compaction should be enabled (Disabled=false) by default")
	}
}

func TestWithDefaultsKeepsUserValues(t *testing.T) {
	in := types.Config{Compaction: types.CompactionConfig{
		MaxContextTokens: 200000, Threshold: 0.5, KeepRecent: 2, Disabled: true,
	}}
	got := withDefaults(in)
	if got.Compaction != in.Compaction {
		t.Fatalf("withDefaults overrode user values: %+v", got.Compaction)
	}
}

func TestLoadWithBaseDirReadsInjectedRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("model: injected-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := LoadWith(LoadOptions{BaseDir: dir})
	if cfg.Model != "injected-model" {
		t.Fatalf("Model = %q, want injected-model", cfg.Model)
	}
	if cfg.BaseDir != dir {
		t.Fatalf("BaseDir = %q, want %q", cfg.BaseDir, dir)
	}
}

func TestLoadWithEmptyBaseDirUsesDefaultPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "nib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "nib", "config.yaml"), []byte("model: xdg-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := LoadWith(LoadOptions{})
	if cfg.Model != "xdg-model" {
		t.Fatalf("Model = %q, want xdg-model", cfg.Model)
	}
}

// The standalone binary must keep writing self-configuration to the first
// existing config file (WritablePath), not to <base>/config.yaml. Load
// therefore leaves BaseDir empty when no root was injected, so every consumer
// that resolves it through WritablePathIn/BaseDirIn gets today's behavior.
func TestLoadWithEmptyBaseDirKeepsWritablePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	if err := os.WriteFile(filepath.Join(home, ".nib.yaml"), []byte("model: home-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := LoadWith(LoadOptions{})
	if got, want := WritablePathIn(cfg.BaseDir), filepath.Join(home, ".nib.yaml"); got != want {
		t.Fatalf("WritablePathIn(cfg.BaseDir) = %q, want %q", got, want)
	}
}

func TestWritablePathInPrefersInjectedRoot(t *testing.T) {
	dir := t.TempDir()
	got := WritablePathIn(dir)
	want := filepath.Join(dir, "config.yaml")
	if got != want {
		t.Fatalf("WritablePathIn = %q, want %q", got, want)
	}
}
