package chat

import "github.com/mudler/cogito"

// WakeupRequest asks the host to re-engage the agent after a delay — an
// in-session reminder/cron the agent schedules for itself. In a self-paced
// loop, Prompt carries the task to repeat; the host re-resolves and re-runs it
// when the delay elapses.
type WakeupRequest struct {
	DelaySeconds int
	Prompt       string // payload to re-run on wake (slash command or prompt)
	Reason       string // one-line "what I'm waiting for", shown to the user
}

type scheduleWakeupArgs struct {
	DelaySeconds int    `json:"delay_seconds" jsonschema:"how long to wait before waking up, in seconds (e.g. 600 for 10 minutes)"`
	Prompt       string `json:"prompt" jsonschema:"the task to run when you wake up — a slash command like /foo or a plain instruction. In a self-paced loop, set this to the task to repeat."`
	Reason       string `json:"reason" jsonschema:"one short line describing what you are waiting for (e.g. 'poll the build job')"`
}

type scheduleWakeupTool struct {
	schedule func(WakeupRequest) string
}

func (t *scheduleWakeupTool) Run(args map[string]any) (string, any, error) {
	delay := 0
	switch v := args["delay_seconds"].(type) {
	case float64:
		delay = int(v)
	case int:
		delay = v
	}
	prompt, _ := args["prompt"].(string)
	if prompt == "" {
		// Back-compat: older callers used `note`.
		prompt, _ = args["note"].(string)
	}
	reason, _ := args["reason"].(string)

	// Clamp to a sane range: at least a few seconds, at most a day.
	if delay < 5 {
		delay = 5
	}
	if delay > 86400 {
		delay = 86400
	}
	if t.schedule == nil {
		return "Scheduling is not available in this session.", nil, nil
	}
	return t.schedule(WakeupRequest{DelaySeconds: delay, Prompt: prompt, Reason: reason}), nil, nil
}

// scheduleWakeupToolDefinition builds the cogito tool definition for
// schedule_wakeup.
func scheduleWakeupToolDefinition(schedule func(WakeupRequest) string) cogito.ToolDefinitionInterface {
	return cogito.NewToolDefinition[map[string]any](
		&scheduleWakeupTool{schedule: schedule},
		scheduleWakeupArgs{},
		"schedule_wakeup",
		"Schedule the session to re-engage you after a delay — an in-session reminder/cron. Returns immediately. When the delay elapses you are re-invoked with your prompt, so you can come back to long-running background work instead of blocking: e.g. read a shell job's output with bash_job_output, list jobs with bash_jobs, or inspect a sub-agent with agent_logs / check_agent / get_agent_result. In a self-paced loop, set `prompt` to the task to repeat; omit this call when the loop should end.",
	)
}
