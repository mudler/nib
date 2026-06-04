package chat

import (
	"reflect"
	"testing"
)

func TestMergeMetadata(t *testing.T) {
	cases := []struct {
		name     string
		global   map[string]string
		override map[string]string
		want     map[string]string
	}{
		{"both empty -> nil", nil, nil, nil},
		{"global only", map[string]string{"enable_thinking": "false"}, nil, map[string]string{"enable_thinking": "false"}},
		{"override only", nil, map[string]string{"enable_thinking": "true"}, map[string]string{"enable_thinking": "true"}},
		{
			"override wins per key, global-only inherited",
			map[string]string{"enable_thinking": "false", "tier": "low"},
			map[string]string{"enable_thinking": "true"},
			map[string]string{"enable_thinking": "true", "tier": "low"},
		},
	}
	for _, c := range cases {
		got := mergeMetadata(c.global, c.override)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: mergeMetadata(%v, %v) = %v, want %v", c.name, c.global, c.override, got, c.want)
		}
	}
}

func TestMergeMetadataDoesNotMutateInputs(t *testing.T) {
	global := map[string]string{"enable_thinking": "false"}
	override := map[string]string{"enable_thinking": "true"}
	_ = mergeMetadata(global, override)
	if global["enable_thinking"] != "false" {
		t.Errorf("global was mutated: %v", global)
	}
	if override["enable_thinking"] != "true" {
		t.Errorf("override was mutated: %v", override)
	}
}

func TestResolveAgentModel(t *testing.T) {
	main := "qwen-main"
	configured := map[string]bool{"qwen-big": true, "qwen-small": true}

	cases := []struct {
		name      string
		requested string
		want      string
	}{
		{"empty falls back to main", "", main},
		{"main is honored", main, main},
		{"configured agent model is honored", "qwen-big", "qwen-big"},
		{"another configured model is honored", "qwen-small", "qwen-small"},
		{"invented model falls back to main", "sonar", main},
		{"unknown model falls back to main", "gpt-4", main},
	}
	for _, c := range cases {
		if got := resolveAgentModel(c.requested, main, configured); got != c.want {
			t.Errorf("%s: resolveAgentModel(%q) = %q, want %q", c.name, c.requested, got, c.want)
		}
	}
}
