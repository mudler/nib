package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
	"gopkg.in/yaml.v3"
)

func TestAnthropicPresetSwitchAndSave(t *testing.T) {
	m := newModel(context.Background(), types.Config{BaseDir: t.TempDir()})
	p := Presets()[0]
	if p.Name != "Anthropic (API key)" || p.Provider != "anthropic-api-key" || !p.KeyRequired {
		t.Fatalf("preset: %+v", p)
	}
	m.applyPreset(p)
	m.collect()
	if m.cfg.Provider != p.Provider {
		t.Fatal("provider lost")
	}
	path := filepath.Join(m.cfg.BaseDir, "config.yaml")
	if err := os.WriteFile(path, []byte("unrelated: retained\nprovider: old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, preset := range []Preset{p, Presets()[1], p, Presets()[11]} {
		m.applyPreset(preset)
		m.collect()
		if m.cfg.Provider != preset.Provider {
			t.Fatal("provider not cleared/set")
		}
		if _, err := Save(m.cfg); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(path)
		var got map[string]any
		if err := yaml.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if got["provider"] != preset.Provider || got["unrelated"] != "retained" {
			t.Fatalf("saved: %v", got)
		}
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0600 {
			t.Fatalf("mode: %v", info.Mode())
		}
	}
}

func TestSetupDiagnosticsMaskKey(t *testing.T) {
	m := newModel(context.Background(), types.Config{})
	m.applyPreset(Presets()[0])
	m.inputs[fieldAPIKey].SetValue("sentinel-setup-private-key")
	m.collect()
	m.saveErr = errors.New("write failed")
	for _, view := range []string{m.viewFields(), m.viewProbe(), m.viewSaved()} {
		if strings.Contains(view, m.cfg.APIKey) {
			t.Fatal("key leaked")
		}
	}
	if !strings.Contains(m.viewSaved(), "anthropic-api-key") {
		t.Fatal("missing manual provider")
	}
}
