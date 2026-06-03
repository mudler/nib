package config

import (
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

// TestDefaultPromptListsAgentTypesAndDelegation verifies the default system
// prompt instructs the model to call tools and enumerates the configured
// sub-agent types so a capable model knows it can delegate.
func TestDefaultPromptListsAgentTypesAndDelegation(t *testing.T) {
	cfg := types.Config{Prompt: defaultPrompt, Agents: MergeAgentTypes(nil)}
	p := cfg.GetPrompt()

	if !strings.Contains(p, "spawn_agent") {
		t.Fatalf("prompt should mention spawn_agent:\n%s", p)
	}
	for _, name := range []string{"general", "explore", "plan"} {
		if !strings.Contains(p, name) {
			t.Fatalf("prompt missing agent type %q:\n%s", name, p)
		}
	}
}
