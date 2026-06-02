package chat

import (
	"testing"

	"github.com/mudler/nib/types"
)

// TestForceReasoningDefaultsOff guards the spec decision: force_reasoning stays
// opt-in (default off) and no reasoning-tool option is introduced.
func TestForceReasoningDefaultsOff(t *testing.T) {
	var opts types.AgentOptions
	if opts.ForceReasoning {
		t.Fatal("ForceReasoning must default to false")
	}
}
