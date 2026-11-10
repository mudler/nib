package chat

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/cogito"
)

// Bounds of wait_for. A wait always ends: with the condition, with a failing
// source, or with its timeout. None of the three is silent.
const (
	waitForDefaultTimeout  = 10 * time.Minute
	waitForMinTimeout      = 10 * time.Second
	waitForMaxTimeout      = 12 * time.Hour
	waitForDefaultInterval = 15
	waitForMinInterval     = 5
	waitForMaxInterval     = 3600
)

type waitForArgs struct {
	Script          string `json:"script" jsonschema:"a shell command to check. Its stdout is the observed value. Set this or url."`
	URL             string `json:"url" jsonschema:"a URL to fetch with GET. The response body is the observed value. Set this or script."`
	Until           string `json:"until" jsonschema:"what to wait for: 'change' (default) = the output differs from the first check; 'match' = the output matches the regular expression in pattern; 'exit_zero' = the script exits with status 0 (script only)"`
	Pattern         string `json:"pattern" jsonschema:"regular expression, for until=match"`
	Then            string `json:"then" jsonschema:"what you should do when the wait ends, so the wake-up carries your next step"`
	TimeoutSeconds  int    `json:"timeout_seconds" jsonschema:"give up after this long (default 600, min 10, max 43200). You are woken at the timeout too."`
	IntervalSeconds int    `json:"interval_seconds" jsonschema:"seconds between checks (default 15, min 5, max 3600)"`
	MaxErrors       int    `json:"max_errors" jsonschema:"end the wait after this many failed checks in a row (default 3)"`
}

type waitForTool struct{ arm func(WaitRequest) string }

func (t *waitForTool) Run(args map[string]any) (string, any, error) {
	if t.arm == nil {
		return "Waiting is not available in this session.", nil, nil
	}
	str := func(k string) string { s, _ := args[k].(string); return s }
	num := func(k string) int { return numArg(args[k]) }
	req := WaitRequest{
		Script:          str("script"),
		URL:             str("url"),
		Until:           str("until"),
		Pattern:         str("pattern"),
		Then:            str("then"),
		Timeout:         clampSeconds(num("timeout_seconds"), waitForDefaultTimeout, waitForMinTimeout, waitForMaxTimeout),
		IntervalSeconds: int(clampSeconds(num("interval_seconds"), waitForDefaultInterval*time.Second, waitForMinInterval*time.Second, waitForMaxInterval*time.Second) / time.Second),
		MaxErrors:       num("max_errors"),
	}
	if req.Script == "" && req.URL == "" {
		return "wait_for needs a script or a url to check.", nil, nil
	}
	if req.Script != "" && req.URL != "" {
		return "wait_for takes a script or a url, not both.", nil, nil
	}
	switch req.Until {
	case "", "change", "match", "exit_zero":
	default:
		return fmt.Sprintf("Unknown until %q: use change, match or exit_zero.", req.Until), nil, nil
	}
	return t.arm(req), nil, nil
}

// clampSeconds turns a number of seconds into a duration inside [min, max]; 0
// or less means def.
func clampSeconds(n int, def, min, max time.Duration) time.Duration {
	d := time.Duration(n) * time.Second
	if n <= 0 {
		d = def
	}
	return min2(max2(d, min), max)
}

func max2(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func min2(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func waitForToolDefinition(arm func(WaitRequest) string) cogito.ToolDefinitionInterface {
	return cogito.NewToolDefinition[map[string]any](
		&waitForTool{arm: arm},
		waitForArgs{},
		"wait_for",
		"Wait for a condition on a command or a URL without blocking: it is armed and the call returns at once. End your turn after you arm it; you are woken when the wait ends, whatever the cause: the condition was met, the source failed several checks in a row, or the timeout passed. A wake-up carries the reason, so read it before you report success. The armed wait shows in cron_list and can be cancelled with cron_delete. Use it to watch a build, a deploy, a file, a page or a service instead of polling by hand. For sub-agents use wait_agents.",
	)
}

// hostWaitForSchema is the schema of wait_for for a host that runs its own
// checks (Callbacks.HostWaits). It has no script and no time defaults: the
// host knows what it allows. A host that also runs scripts
// (Callbacks.HostWaitScripts) gets hostWaitForScriptSchema.
var hostWaitForSchema = buildHostWaitForSchema(false, false)

// hostWaitForScriptSchema is hostWaitForSchema with the script source and the
// exit_zero condition.
var hostWaitForScriptSchema = buildHostWaitForSchema(true, false)

// Bounds of the idle time of until=stall (Callbacks.HostWaitJobs).
const (
	waitForDefaultIdle = 600
	waitForMinIdle     = 60
	waitForMaxIdle     = 3600
)

func buildHostWaitForSchema(scripts, jobs bool) map[string]any {
	until := []string{"change", "match", "json_equals"}
	untilDesc := "change (default) = the output differs from the first check; match = the output matches the regular expression in pattern; json_equals = the JSON field of the output equals the value."
	if scripts {
		until = append(until, "exit_zero")
		untilDesc += " exit_zero = the script exits with status 0 (script only). A non-zero exit is not an error: it is part of the observation, and the wait goes on."
	}
	if jobs {
		until = append(until, "exit", "stall")
		untilDesc += " exit = the background job has exited; the wake-up carries its exit code and the tail of its output (job only). stall = the job is still running but has written no output for idle seconds (job only). For a job, match looks at the output the job wrote since the last check."
	}
	props := map[string]any{
		"tool":             map[string]any{"type": "string", "description": "Name of a read-only tool of this session to poll, such as one that lists downloads."},
		"arguments":        map[string]any{"type": "object", "description": "The fixed arguments of that tool."},
		"url":              map[string]any{"type": "string", "description": "An http or https address to poll instead of a tool."},
		"until":            map[string]any{"type": "string", "enum": until, "description": untilDesc},
		"pattern":          map[string]any{"type": "string", "description": "For match: a regular expression the output must match."},
		"field":            map[string]any{"type": "string", "description": "For json_equals: the field of the JSON output, with dots for depth and numbers for list items, such as items.0.status."},
		"equals":           map[string]any{"description": "For json_equals: the value the field must equal, such as \"done\"."},
		"interval_seconds": map[string]any{"type": "integer", "description": "Seconds between checks. The host sets a minimum."},
		"timeout_seconds":  map[string]any{"type": "integer", "description": "Give up after this many seconds. You are woken at the timeout too."},
		"max_errors":       map[string]any{"type": "integer", "minimum": 1, "description": "End the wait after this many failed checks in a row."},
		"then":             map[string]any{"type": "string", "description": "What you should do when the wait ends. It must make sense without the rest of this turn."},
		"reason":           map[string]any{"type": "string", "maxLength": 300, "description": "One short line about what you wait for, which the person sees."},
	}
	if scripts {
		props["script"] = map[string]any{"type": "string", "description": "A shell command to poll instead of a tool or a url. Its standard output is the observed value, and its exit status is observed too. A command that exits with a non-zero status is not a failed check."}
	}
	if jobs {
		props["job"] = map[string]any{"type": "string", "description": "The id of a background job you started earlier with the job tool, to watch instead of a tool, a url or a script. Use it to wait for that job to exit, for a pattern in its output, or for it to stall."}
		props["idle"] = map[string]any{"type": "integer", "minimum": waitForMinIdle, "maximum": waitForMaxIdle, "default": waitForDefaultIdle, "description": "For stall: seconds without any output from the job before you are woken (min 60, max 3600, default 600)."}
	}
	return map[string]any{"type": "object", "properties": props, "required": []string{"then"}}
}

const hostWaitForDescription = "Wait for something to happen without spending a model call on each check: the server checks for you and wakes you once. " +
	"Use it when you tell the person that you will wait until a download or a job is done, or until a page or a tool's output changes. " +
	"The source is one of: tool (a read-only tool of this session, with fixed arguments) or url (a page). " +
	"You are woken when the condition holds, when the source fails max_errors checks in a row, or when the timeout passes; the wake-up text says plainly when the condition was NOT met. " +
	"End your turn after you arm it. The person sees the wait and can stop it."

const hostWaitForScriptDescription = "Wait for something to happen without spending a model call on each check: the server checks for you and wakes you once. " +
	"Use it when you tell the person that you will wait until a download or a job is done, or until a page, a tool's output or a command's output changes. " +
	"The source is one of: tool (a read-only tool of this session, with fixed arguments), url (a page) or script (a shell command). " +
	"You are woken when the condition holds, when the source fails max_errors checks in a row, or when the timeout passes; the wake-up text says plainly when the condition was NOT met. " +
	"End your turn after you arm it. The person sees the wait and can stop it."

const hostWaitForJobDescription = " A background job you started earlier can be the source too: use job with until=exit to be woken when it exits, with until=match for a pattern in its output, or with until=stall to be woken when it is still running but has written nothing for a while."

// hostWaitForTool is wait_for for a host that runs its own checks. scripts is
// true when the host also runs shell commands (Callbacks.HostWaitScripts), jobs
// when it also watches background jobs (Callbacks.HostWaitJobs).
type hostWaitForTool struct {
	arm     func(WaitRequest) string
	scripts bool
	jobs    bool
}

func (t *hostWaitForTool) Run(args map[string]any) (string, any, error) {
	str := func(k string) string { s, _ := args[k].(string); return strings.TrimSpace(s) }
	num := func(k string) int { return numArg(args[k]) }
	req := WaitRequest{
		Tool:            str("tool"),
		URL:             str("url"),
		Until:           str("until"),
		Pattern:         str("pattern"),
		Field:           str("field"),
		Equals:          args["equals"],
		Then:            str("then"),
		Reason:          str("reason"),
		Timeout:         time.Duration(num("timeout_seconds")) * time.Second,
		IntervalSeconds: num("interval_seconds"),
		MaxErrors:       num("max_errors"),
	}
	req.Arguments, _ = args["arguments"].(map[string]any)
	req.Script = str("script")
	if t.jobs {
		req.Job = str("job")
		req.Idle = time.Duration(num("idle")) * time.Second
		return t.runJobs(req)
	}
	if !t.scripts {
		if req.Script != "" {
			return "wait_for cannot run a script in this session: use a tool or a url.", nil, nil
		}
		if req.Tool == "" && req.URL == "" {
			return "wait_for needs a tool or a url to check.", nil, nil
		}
		if req.Tool != "" && req.URL != "" {
			return "wait_for takes a tool or a url, not both.", nil, nil
		}
	} else {
		n := 0
		for _, v := range []string{req.Tool, req.URL, req.Script} {
			if v != "" {
				n++
			}
		}
		if n == 0 {
			return "wait_for needs a tool, a url or a script to check.", nil, nil
		}
		if n > 1 {
			return "wait_for takes one of tool, url or script, not several.", nil, nil
		}
	}
	switch req.Until {
	case "", "change", "match", "json_equals":
	case "exit_zero":
		if !t.scripts || req.Script == "" {
			return "until=exit_zero needs a script to check.", nil, nil
		}
	default:
		if t.scripts {
			return fmt.Sprintf("Unknown until %q: use change, match, json_equals or exit_zero.", req.Until), nil, nil
		}
		return fmt.Sprintf("Unknown until %q: use change, match or json_equals.", req.Until), nil, nil
	}
	if req.Then == "" {
		return "wait_for needs then: say what to do when the wait ends.", nil, nil
	}
	return t.arm(req), nil, nil
}

// runJobs validates and arms a request when the host watches jobs.
func (t *hostWaitForTool) runJobs(req WaitRequest) (string, any, error) {
	if req.Script != "" && !t.scripts {
		return "wait_for cannot run a script in this session: use a tool, a url or a job.", nil, nil
	}
	names := "tool, url or job"
	sources := []string{req.Tool, req.URL, req.Job}
	if t.scripts {
		names = "tool, url, script or job"
		sources = append(sources, req.Script)
	}
	n := 0
	for _, v := range sources {
		if v != "" {
			n++
		}
	}
	if n == 0 {
		return fmt.Sprintf("wait_for needs a %s to check.", names), nil, nil
	}
	if n > 1 {
		return fmt.Sprintf("wait_for takes one of %s, not several.", names), nil, nil
	}
	known := "change, match, json_equals, exit or stall"
	if t.scripts {
		known = "change, match, json_equals, exit_zero, exit or stall"
	}
	switch req.Until {
	case "", "change", "match", "json_equals":
	case "exit_zero":
		if !t.scripts || req.Script == "" {
			return "until=exit_zero needs a script to check.", nil, nil
		}
	case "exit", "stall":
		if req.Job == "" {
			return fmt.Sprintf("until=%s needs a job to check.", req.Until), nil, nil
		}
	default:
		return fmt.Sprintf("Unknown until %q: use %s.", req.Until, known), nil, nil
	}
	if req.Idle != 0 && req.Until != "stall" {
		return "idle is only for until=stall on a job.", nil, nil
	}
	if req.Until == "stall" {
		if req.Idle == 0 {
			req.Idle = waitForDefaultIdle * time.Second
		}
		if req.Idle < waitForMinIdle*time.Second || req.Idle > waitForMaxIdle*time.Second {
			return fmt.Sprintf("idle must be between %d and %d seconds.", waitForMinIdle, waitForMaxIdle), nil, nil
		}
	}
	if req.Then == "" {
		return "wait_for needs then: say what to do when the wait ends.", nil, nil
	}
	return t.arm(req), nil, nil
}

// hostWaitForToolDefinition builds wait_for for a host. scripts offers the
// script source and the exit_zero condition. jobs offers the job source and
// the exit and stall conditions.
func hostWaitForToolDefinition(arm func(WaitRequest) string, scripts, jobs bool) cogito.ToolDefinitionInterface {
	if jobs {
		desc := hostWaitForDescription
		if scripts {
			desc = hostWaitForScriptDescription
		}
		return cogito.NewToolDefinition[map[string]any](&hostWaitForTool{arm: arm, scripts: scripts, jobs: true}, buildHostWaitForSchema(scripts, true), "wait_for", desc+hostWaitForJobDescription)
	}
	if scripts {
		return cogito.NewToolDefinition[map[string]any](&hostWaitForTool{arm: arm, scripts: true}, hostWaitForScriptSchema, "wait_for", hostWaitForScriptDescription)
	}
	return cogito.NewToolDefinition[map[string]any](&hostWaitForTool{arm: arm}, hostWaitForSchema, "wait_for", hostWaitForDescription)
}

// numArg reads a number argument. The tool loop decodes numbers as
// json.Number, a test or a host may pass float64 or int.
func numArg(v any) int {
	switch n := v.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
		f, _ := n.Float64()
		return int(f)
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}
