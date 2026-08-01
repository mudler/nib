package setup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/nib/types"
	"gopkg.in/yaml.v3"
)

func TestSaveWritesConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	path, err := Save(types.Config{Model: "gpt-4o-mini", APIKey: "sk-abc", BaseURL: "https://api.openai.com/v1"})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	want := filepath.Join(tmp, "nib", "config.yaml")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}

	data, _ := os.ReadFile(path)
	var got map[string]any
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["model"] != "gpt-4o-mini" || got["api_key"] != "sk-abc" || got["base_url"] != "https://api.openai.com/v1" {
		t.Errorf("written config = %v", got)
	}
}

func TestSavePreservesExistingKeys(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	dir := filepath.Join(tmp, "nib")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("log_level: debug\nmodel: old\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Save(types.Config{Model: "new", APIKey: "k", BaseURL: "u"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	var got map[string]any
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["log_level"] != "debug" {
		t.Errorf("unrelated key lost: %v", got)
	}
	if got["model"] != "new" {
		t.Errorf("model not overwritten: %v", got)
	}
}

// An embedded nib runs onboarding against an injected root. Save must write
// there, not into the user's real ~/.config/nib: the embedder's model and, more
// importantly, its api_key would otherwise land in the user's own config.
func TestSaveHonorsBaseDirOverride(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	root := t.TempDir()

	path, err := Save(types.Config{Model: "embedded", APIKey: "sk-embedded", BaseURL: "u", BaseDir: root})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if want := filepath.Join(root, "config.yaml"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	if _, err := os.Stat(filepath.Join(xdg, "nib", "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("wrote into the user's real nib root: err = %v", err)
	}

	data, _ := os.ReadFile(path)
	var got map[string]any
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["api_key"] != "sk-embedded" {
		t.Fatalf("written config = %v", got)
	}
	// BaseDir is runtime-only and must never be serialized into the file.
	if _, ok := got["base_dir"]; ok {
		t.Fatalf("base_dir leaked into the config file: %v", got)
	}
}

// The wizard collects only the three connection fields, so the override has to
// survive newModel or Save would fall back to the default root.
func TestWizardCarriesBaseDirIntoSavedConfig(t *testing.T) {
	root := t.TempDir()
	m := newModel(context.Background(), types.Config{BaseDir: root})
	m.collect()
	if m.cfg.BaseDir != root {
		t.Fatalf("wizard dropped the override: BaseDir = %q, want %q", m.cfg.BaseDir, root)
	}
}
