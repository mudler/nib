package setup

import "testing"

func TestPresets(t *testing.T) {
	ps := Presets()
	if len(ps) != 4 {
		t.Fatalf("want 4 presets, got %d", len(ps))
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

	if !seen["OpenAI"] || !seen["Ollama"] || !seen["Custom"] {
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
