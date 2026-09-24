package chat

import "github.com/mudler/cogito"

type cronArgs struct {
	Expr      string `json:"expr" jsonschema:"5- or 6-field cron in local time: '[S] M H DoM Mon DoW' — a 6th leading field adds seconds (e.g. '*/10 * * * * *' = every 10s, '*/5 * * * *' = every 5 min, '0 9 * * 1-5' = weekdays 9am)"`
	Prompt    string `json:"prompt" jsonschema:"the task to run at each fire — a slash command like /foo or a plain instruction"`
	Recurring bool   `json:"recurring" jsonschema:"true = fire on every match (default); false = fire once then auto-delete"`
	Durable   bool   `json:"durable" jsonschema:"true = persist across restarts to .nib/loops.json; false = session-only (default)"`
	MonitorScript string `json:"monitor_script" jsonschema:"optional. A shell command whose stdout is hashed each tick; the agent runs only when the hash changes. Mutually exclusive with monitor_url."`
	MonitorURL    string `json:"monitor_url" jsonschema:"optional. A URL whose response body is hashed each tick. Mutually exclusive with monitor_script."`
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
	monitorScript, _ := args["monitor_script"].(string)
	monitorURL, _ := args["monitor_url"].(string)
	if t.create == nil {
		return "Scheduling is not available in this session.", nil, nil
	}
	return t.create(CronRequest{Expr: expr, Prompt: prompt, Recurring: recurring, Durable: durable, MonitorScript: monitorScript, MonitorURL: monitorURL}), nil, nil
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

// --- cron_pause / cron_resume / cron_trigger ---

// cronIDTool runs fn on the job id it is given. It backs the cron tools that
// act on one existing job.
type cronIDTool struct{ fn func(string) string }

func (t *cronIDTool) Run(args map[string]any) (string, any, error) {
	id, _ := args["id"].(string)
	if t.fn == nil {
		return "No scheduler available.", nil, nil
	}
	return t.fn(id), nil, nil
}

func cronIDToolDefinition(name, description string, fn func(string) string) cogito.ToolDefinitionInterface {
	return cogito.NewToolDefinition[map[string]any](&cronIDTool{fn: fn}, cronDeleteArgs{}, name, description)
}

func cronPauseToolDefinition(pause func(string) string) cogito.ToolDefinitionInterface {
	return cronIDToolDefinition("cron_pause",
		"Pause a cron job by id: it stops firing but stays registered. Resume it with cron_resume.", pause)
}

func cronResumeToolDefinition(resume func(string) string) cogito.ToolDefinitionInterface {
	return cronIDToolDefinition("cron_resume",
		"Resume a cron job paused with cron_pause. It fires at its next scheduled time; slots missed while paused are skipped.", resume)
}

func cronTriggerToolDefinition(trigger func(string) string) cogito.ToolDefinitionInterface {
	return cronIDToolDefinition("cron_trigger",
		"Run a cron job's prompt now, by id, without changing its schedule. The prompt runs after the current turn.", trigger)
}
