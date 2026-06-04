package chat

import (
	"testing"

	"github.com/mudler/cogito"
)

func TestAgentUsage(t *testing.T) {
	t.Run("nil fragment", func(t *testing.T) {
		tc, tok := agentUsage(&cogito.AgentState{})
		if tc != 0 || tok != 0 {
			t.Fatalf("got (%d, %d), want (0, 0)", tc, tok)
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
		tc, tok := agentUsage(a)
		if tc != 3 || tok != 12400 {
			t.Fatalf("got (%d, %d), want (3, 12400)", tc, tok)
		}
	})
}
