package config

import (
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

// Delegation advice and advertised personas belong to the capability-gated
// chat guidance, not the unconditional default prompt, even with agents configured.
func TestDefaultPromptHasNoUnconditionalDelegation(t *testing.T) {
	cfg := types.Config{Prompt: defaultPrompt, Agents: MergeAgentTypes(nil)}
	p := cfg.GetPrompt()

	for _, advice := range []string{"spawn_agent", "delegate to a sub-agent", "Available sub-agent types:"} {
		if strings.Contains(p, advice) {
			t.Fatalf("prompt contains unconditional delegation advice %q:\n%s", advice, p)
		}
	}
	for _, agent := range cfg.Agents {
		if strings.Contains(p, "- "+agent.Name+": "+agent.Description) {
			t.Fatalf("prompt unconditionally advertises agent type %q:\n%s", agent.Name, p)
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
