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

// The line moved to types.toolGuidance, which GetPrompt appends unconditionally.
// Leaving a copy in defaultPrompt would render it twice for default-prompt
// users, which is how a model learns an instruction is boilerplate.
func TestDefaultPromptNoLongerCarriesTheActLine(t *testing.T) {
	if strings.Contains(defaultPrompt, "Always act by CALLING") {
		t.Fatal("defaultPrompt still carries the act-don't-narrate line; it now lives in types.toolGuidance and would render twice")
	}
}

// The move must not lose it: a default-prompt session still receives it, via
// the appended block rather than the template.
func TestDefaultPromptSessionStillGetsTheActLine(t *testing.T) {
	cfg := types.Config{Prompt: defaultPrompt, Agents: MergeAgentTypes(nil)}
	p := cfg.GetPrompt()

	if !strings.Contains(p, "Always act by CALLING the available tools") {
		t.Fatalf("the act-don't-narrate line was lost in the move:\n%s", p)
	}
	if strings.Count(p, "Always act by CALLING") != 1 {
		t.Fatalf("the line renders %d times, want exactly 1:\n%s", strings.Count(p, "Always act by CALLING"), p)
	}
}
