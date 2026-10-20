package setup

import "testing"

func TestPresets(t *testing.T) {
	ps := Presets()
	if len(ps) != 13 {
		t.Fatalf("want 13 presets, got %d", len(ps))
	}

	seen := map[string]bool{}
	for _, p := range ps {
		if p.Name == "" {
			t.Errorf("preset has empty name: %+v", p)
		}
		if seen[p.Name] {
			t.Errorf("duplicate preset name %q", p.Name)
		}
		seen[p.Name] = true
	}

	if !seen["Anthropic (Claude Pro/Max)"] || !seen["Anthropic (API key)"] || !seen["OpenAI"] || !seen["Ollama"] || !seen["Custom"] {
		t.Fatalf("missing an expected preset: %v", seen)
	}

	// Ollama prefills a local OpenAI-compatible endpoint and a default model.
	var ollama Preset
	for _, p := range ps {
		if p.Name == "Ollama" {
			ollama = p
		}
	}
	if ollama.BaseURL != "http://localhost:11434/v1" {
		t.Errorf("ollama base url = %q", ollama.BaseURL)
	}
	if ollama.DefaultModel == "" {
		t.Errorf("ollama should prefill a default model")
	}

	// Custom is intentionally blank in every field.
	last := ps[len(ps)-1]
	if last.Name != "Custom" || last.BaseURL != "" || last.DefaultModel != "" || last.DefaultKey != "" {
		t.Errorf("Custom preset should be all-empty, got %+v", last)
	}
}

// Look up by name so adding a preset cannot silently change a test's provider.
func presetNamed(t *testing.T, name string) Preset {
	t.Helper()
	for _, p := range Presets() {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("missing preset %q", name)
	return Preset{}
}

func TestAnthropicPresetsAreSeparate(t *testing.T) {
	for _, tc := range []struct {
		name, provider string
		required       bool
	}{
		{"Anthropic (Claude Pro/Max)", "anthropic", false},
		{"Anthropic (API key)", "anthropic-api-key", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := presetNamed(t, tc.name)
			if p.Provider != tc.provider || p.KeyRequired != tc.required || p.DefaultKey != "" || p.BaseURL != "https://api.anthropic.com" || p.DefaultModel != "claude-sonnet-4-5-20250929" {
				t.Fatalf("incorrect preset: %+v", p)
			}
		})
	}
}
