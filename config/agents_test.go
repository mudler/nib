package config

import "testing"

func TestDefaultAgentTypesPresent(t *testing.T) {
	defs := MergeAgentTypes(nil)
	if len(defs) == 0 {
		t.Fatal("expected built-in agent types")
	}
	if findType(defs, "explore") == nil || findType(defs, "plan") == nil {
		t.Fatalf("expected default explore+plan types, got %v", names(defs))
	}
}

func TestYAMLOverridesByName(t *testing.T) {
	override := []AgentTypeConfig{
		{Name: "explore", SystemPrompt: "CUSTOM EXPLORE", Model: "small"},
		{Name: "custom", Description: "user type", SystemPrompt: "hi"},
	}
	defs := MergeAgentTypes(override)
	ex := findType(defs, "explore")
	if ex == nil || ex.SystemPrompt != "CUSTOM EXPLORE" || ex.Model != "small" {
		t.Fatalf("explore not overridden: %+v", ex)
	}
	if findType(defs, "custom") == nil {
		t.Fatal("custom type not added")
	}
	// Non-overridden defaults survive.
	if findType(defs, "plan") == nil {
		t.Fatal("plan default lost after override")
	}
}

// test helpers
func findType(defs []AgentTypeConfig, name string) *AgentTypeConfig {
	for i := range defs {
		if defs[i].Name == name {
			return &defs[i]
		}
	}
	return nil
}
func names(defs []AgentTypeConfig) []string {
	var n []string
	for _, d := range defs {
		n = append(n, d.Name)
	}
	return n
}
