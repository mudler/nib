package chat

import (
	"testing"

	"github.com/mudler/cogito"
)

func TestAgentUsageFullToolCountAndTotal(t *testing.T) {
	t.Run("nil fragment", func(t *testing.T) {
		tc, u := agentUsageFull(&cogito.AgentState{})
		if tc != 0 || u.TotalTokens != 0 {
			t.Fatalf("got (%d, %d), want (0, 0)", tc, u.TotalTokens)
		}
	})

	t.Run("populated", func(t *testing.T) {
		a := &cogito.AgentState{
			Fragment: &cogito.Fragment{
				Status: &cogito.Status{
					ToolsCalled:     make(cogito.Tools, 3),
					CumulativeUsage: cogito.LLMUsage{TotalTokens: 12400},
				},
			},
		}
		tc, u := agentUsageFull(a)
		if tc != 3 || u.TotalTokens != 12400 {
			t.Fatalf("got (%d, %d), want (3, 12400)", tc, u.TotalTokens)
		}
	})
}
