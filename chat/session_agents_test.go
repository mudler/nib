package chat

import (
	"testing"

	"github.com/mudler/cogito"
	"github.com/mudler/wiz/types"
)

func TestToCogitoDefinitionsMapsFields(t *testing.T) {
	in := []types.AgentTypeConfig{{
		Name: "explore", Description: "d", SystemPrompt: "sp",
		Tools: []string{"echo"}, Model: "m", Temperature: 0.4,
		Iterations: 9, MaxAttempts: 2, MaxRetries: 1,
	}}
	out := toCogitoDefinitions(in)
	if len(out) != 1 {
		t.Fatalf("want 1 def, got %d", len(out))
	}
	d := out[0]
	if d.Name != "explore" || d.SystemPrompt != "sp" || d.Model != "m" ||
		d.Temperature != 0.4 || d.Iterations != 9 || d.MaxAttempts != 2 || d.MaxRetries != 1 ||
		len(d.Tools) != 1 || d.Tools[0] != "echo" {
		t.Fatalf("mapping wrong: %+v", d)
	}
	var _ cogito.AgentDefinition = d
}
