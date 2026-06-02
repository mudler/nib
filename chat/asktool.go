package chat

import "github.com/mudler/cogito"

// askUserArgs is the JSON-schema shape of the ask_user tool's parameters.
type askUserArgs struct {
	Question    string   `json:"question" jsonschema:"the question to ask the user"`
	Options     []string `json:"options,omitempty" jsonschema:"optional list of choices the user can pick from"`
	MultiSelect bool     `json:"multi_select,omitempty" jsonschema:"when true the user may pick several options (checkbox); when false or omitted they pick exactly one (radio). Only meaningful with options."`
}

// askUserTool is a cogito tool that asks the user a question and returns the
// answer. It satisfies cogito.Tool[map[string]any].
type askUserTool struct {
	ask func(AskRequest) string
}

// Run parses the tool arguments, asks the user, and returns the answer string.
func (a *askUserTool) Run(args map[string]any) (string, any, error) {
	q, _ := args["question"].(string)
	var opts []string
	switch v := args["options"].(type) {
	case []any:
		for _, o := range v {
			if s, ok := o.(string); ok {
				opts = append(opts, s)
			}
		}
	case []string:
		opts = v
	}
	multi, _ := args["multi_select"].(bool)
	if a.ask == nil {
		return "", nil, nil
	}
	return a.ask(AskRequest{Question: q, Options: opts, MultiSelect: multi}), nil, nil
}

// askUserToolDefinition builds the cogito tool definition for ask_user.
func askUserToolDefinition(ask func(AskRequest) string) cogito.ToolDefinitionInterface {
	return cogito.NewToolDefinition[map[string]any](
		&askUserTool{ask: ask},
		askUserArgs{},
		"ask_user",
		"Ask the user a clarifying question and wait for their answer. Provide `options` for a multiple-choice question (omit them for free-text), and set `multi_select` to true when several options may be chosen (checkbox) rather than exactly one (radio). Use this when you need information only the user can provide.",
	)
}
