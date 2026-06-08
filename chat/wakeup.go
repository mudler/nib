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
	// Poll marks a wake-up whose only purpose is to poll background work already
	// in flight (a sub-agent or shell job). Such a wake-up is auto-cancelled if
	// that work finishes first, since the run is then resumed with the result
	// automatically — leaving the poll tick to re-dispatch the finished task.
	// Reminders and self-paced loop steps leave this false so they always fire.
	Poll bool
}

type scheduleWakeupArgs struct {
	DelaySeconds int    `json:"delay_seconds" jsonschema:"how long to wait before waking up, in seconds (e.g. 600 for 10 minutes)"`
	Prompt       string `json:"prompt" jsonschema:"the task to run when you wake up — a slash command like /foo or a plain instruction. In a self-paced loop, set this to the task to repeat."`
	Reason       string `json:"reason" jsonschema:"one short line describing what you are waiting for (e.g. 'poll the build job')"`
	Polling      bool   `json:"polling,omitempty" jsonschema:"set true ONLY if this wake-up exists solely to poll background work you already started (a sub-agent or shell job); it is then auto-cancelled if that work finishes first, since you are resumed with its result automatically. Leave false for reminders or self-paced loop steps so they always fire. If omitted, it defaults to true while background work is running and false otherwise."`
}

type scheduleWakeupTool struct {
	schedule func(WakeupRequest) string
	// pending reports whether background work is in flight right now; it seeds the
	// poll default when the model omits `polling`. May be nil (treated as no work).
	pending func() bool
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

	// Poll-ness: default from whether background work is running now, so the
	// common "come back and check my sub-agent" case is auto-cancelled when that
	// work completes (cogito resumes us with the result, making the poll a
	// redundant re-dispatch). The model may override either way via `polling`.
	poll := false
	if t.pending != nil {
		poll = t.pending()
	}
	switch v := args["polling"].(type) {
	case bool:
		poll = v
	case string:
		poll = v == "true"
	}

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
	return t.schedule(WakeupRequest{DelaySeconds: delay, Prompt: prompt, Reason: reason, Poll: poll}), nil, nil
}

// scheduleWakeupToolDefinition builds the cogito tool definition for
// schedule_wakeup.
func scheduleWakeupToolDefinition(schedule func(WakeupRequest) string, pending func() bool) cogito.ToolDefinitionInterface {
	return cogito.NewToolDefinition[map[string]any](
		&scheduleWakeupTool{schedule: schedule, pending: pending},
		scheduleWakeupArgs{},
		"schedule_wakeup",
		"Schedule the session to re-engage you after a delay — an in-session reminder/cron. Returns immediately. When the delay elapses you are re-invoked with your prompt, so you can come back to long-running background work instead of blocking: e.g. read a shell job's output with bash_job_output, list jobs with bash_jobs, or inspect a sub-agent with agent_logs / check_agent / get_agent_result. In a self-paced loop, set `prompt` to the task to repeat; omit this call when the loop should end.",
	)
}
