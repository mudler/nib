package chat

import (
	"fmt"

	"github.com/mudler/cogito"
)

// goalDoneArgs is the argument schema for the goal_done tool. Map-free per the
// cogito tool-schema constraint.
type goalDoneArgs struct {
	Justification string `json:"justification" jsonschema:"one short line stating how the goal is now satisfied"`
}

// goalDoneTool lets the model declare the active goal met. The host callback
// records completion (clears the goal and lets the turn end).
type goalDoneTool struct {
	onDone func(justification string) string
}

func (t *goalDoneTool) Run(args map[string]any) (string, any, error) {
	justification, _ := args["justification"].(string)
	if t.onDone == nil {
		return "Goal tracking is not available in this session.", nil, nil
	}
	return t.onDone(justification), nil, nil
}

// goalDoneToolDefinition builds the cogito tool definition for goal_done.
func goalDoneToolDefinition(onDone func(justification string) string) cogito.ToolDefinitionInterface {
	return cogito.NewToolDefinition[map[string]any](
		&goalDoneTool{onDone: onDone},
		goalDoneArgs{},
		"goal_done",
		"Declare the current goal fully met. Call this ONLY when you have genuinely achieved the goal — it ends your turn. Provide a one-line justification of how the goal is satisfied.",
	)
}

// goalReminder is injected when a turn ends while a goal is still active, to
// make the model verify completion and either keep working or call goal_done.
func goalReminder(goal string) string {
	return fmt.Sprintf(
		"Before you stop: verify you have genuinely met this goal — %q. "+
			"If it is fully met, call the `goal_done` tool with a one-line justification. "+
			"Otherwise keep working toward it.",
		goal,
	)
}
