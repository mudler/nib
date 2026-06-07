package chat

import "github.com/mudler/cogito"

type cronArgs struct {
	Expr      string `json:"expr" jsonschema:"5-field cron expression in local time: 'M H DoM Mon DoW' (e.g. '*/5 * * * *' = every 5 minutes, '0 9 * * 1-5' = weekdays 9am)"`
	Prompt    string `json:"prompt" jsonschema:"the task to run at each fire — a slash command like /foo or a plain instruction"`
	Recurring bool   `json:"recurring" jsonschema:"true = fire on every match (default); false = fire once then auto-delete"`
	Durable   bool   `json:"durable" jsonschema:"true = persist across restarts to .nib/loops.json; false = session-only (default)"`
}

type cronTool struct{ create func(CronRequest) string }

func (t *cronTool) Run(args map[string]any) (string, any, error) {
	expr, _ := args["expr"].(string)
	prompt, _ := args["prompt"].(string)
	recurring := true
	if v, ok := args["recurring"].(bool); ok {
		recurring = v
	}
	durable, _ := args["durable"].(bool)
	if t.create == nil {
		return "Scheduling is not available in this session.", nil, nil
	}
	return t.create(CronRequest{Expr: expr, Prompt: prompt, Recurring: recurring, Durable: durable}), nil, nil
}

func cronToolDefinition(create func(CronRequest) string) cogito.ToolDefinitionInterface {
	return cogito.NewToolDefinition[map[string]any](
		&cronTool{create: create},
		cronArgs{},
		"cron",
		"Schedule a prompt to run on a recurring cron schedule (or once). Returns a job id. Jobs fire only while the session is idle (queued otherwise). Use cron_list to see active jobs and cron_delete to cancel one. Session-only unless durable is true.",
	)
}

type cronListArgs struct{}

type cronListTool struct{ list func() string }

func (t *cronListTool) Run(args map[string]any) (string, any, error) {
	if t.list == nil {
		return "No scheduler available.", nil, nil
	}
	return t.list(), nil, nil
}

func cronListToolDefinition(list func() string) cogito.ToolDefinitionInterface {
	return cogito.NewToolDefinition[map[string]any](
		&cronListTool{list: list},
		cronListArgs{},
		"cron_list",
		"List active cron jobs scheduled with the cron tool (id, schedule, next fire, prompt).",
	)
}

type cronDeleteArgs struct {
	ID string `json:"id" jsonschema:"the job id returned by cron"`
}

type cronDeleteTool struct{ del func(string) string }

func (t *cronDeleteTool) Run(args map[string]any) (string, any, error) {
	id, _ := args["id"].(string)
	if t.del == nil {
		return "No scheduler available.", nil, nil
	}
	return t.del(id), nil, nil
}

func cronDeleteToolDefinition(del func(string) string) cogito.ToolDefinitionInterface {
	return cogito.NewToolDefinition[map[string]any](
		&cronDeleteTool{del: del},
		cronDeleteArgs{},
		"cron_delete",
		"Cancel a cron job previously scheduled with the cron tool, by id.",
	)
}
