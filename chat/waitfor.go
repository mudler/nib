package chat

import (
	"fmt"
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
	num := func(k string) int {
		switch v := args[k].(type) {
		case float64:
			return int(v)
		case int:
			return v
		}
		return 0
	}
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
