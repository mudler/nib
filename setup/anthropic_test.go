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
	p := presetNamed(t, "Anthropic (API key)")
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
	for _, preset := range []Preset{p, presetNamed(t, "Anthropic (Claude Pro/Max)"), p, presetNamed(t, "OpenAI"), p, presetNamed(t, "Custom")} {
		m.inputs[fieldAPIKey].SetValue("stale-key-sentinel")
		m.cfg.Provider = "stale-provider"
		m.applyPreset(preset)
		m.collect()
		if m.cfg.APIKey != preset.DefaultKey || m.keyRequired != preset.KeyRequired {
			t.Fatal("stale key or key hint survived switching")
		}
		if strings.Contains(m.viewFields(), "This provider requires an API key.") != preset.KeyRequired {
			t.Fatal("incorrect key hint")
		}
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
		if got["provider"] != preset.Provider || got["api_key"] != preset.DefaultKey || got["unrelated"] != "retained" {
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
	m.applyPreset(presetNamed(t, "Anthropic (API key)"))
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
