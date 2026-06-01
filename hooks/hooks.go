// Package hooks runs shell-command hooks bound to lifecycle events. Each hook
// receives the event payload as JSON on stdin and may return a JSON Decision on
// stdout; PreToolUse decisions gate tool execution.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/mudler/wiz/types"
)

// Event identifies a lifecycle hook point.
type Event string

const (
	EventSessionStart     Event = "SessionStart"
	EventUserPromptSubmit Event = "UserPromptSubmit"
	EventPreToolUse       Event = "PreToolUse"
	EventPostToolUse      Event = "PostToolUse"
	EventAgentEvent       Event = "AgentEvent"
	EventStop             Event = "Stop"
)

// Decision is a hook's JSON stdout response.
type Decision struct {
	Approved   *bool  `json:"approved"`
	Block      bool   `json:"block"`
	Adjustment string `json:"adjustment"`
	Reason     string `json:"reason"`
}

// ToolDecision is the combined PreToolUse verdict.
type ToolDecision struct {
	Decided    bool
	Approve    bool
	Adjustment string
	Reason     string
}

// Dispatcher fires hooks for events.
type Dispatcher struct {
	hooks   []types.HookConfig
	timeout time.Duration
}

// New builds a dispatcher over the given hooks (a nil/empty slice is a no-op).
func New(hooks []types.HookConfig) *Dispatcher {
	return &Dispatcher{hooks: hooks, timeout: 30 * time.Second}
}

// Fire runs every hook whose Event matches and whose Matcher matches name
// (empty matcher matches all), passing payload as JSON on stdin, and returns
// each hook's Decision in order.
func (d *Dispatcher) Fire(ctx context.Context, event Event, name string, payload any) []Decision {
	if d == nil || len(d.hooks) == 0 {
		return nil
	}
	data, _ := json.Marshal(payload)
	var out []Decision
	for _, h := range d.hooks {
		if Event(h.Event) != event || !matchHook(h.Matcher, name) {
			continue
		}
		out = append(out, runHook(ctx, h, data, d.timeout))
	}
	return out
}

// matchHook reports whether a hook matcher matches name. Empty matches all; a
// valid regexp is matched; otherwise exact equality.
func matchHook(pattern, name string) bool {
	if strings.TrimSpace(pattern) == "" {
		return true
	}
	if pattern == name {
		return true
	}
	if re, err := regexp.Compile(pattern); err == nil {
		return re.MatchString(name)
	}
	return false
}

// runHook executes one hook and returns its Decision. A non-zero exit (or
// timeout) is treated as Block, with stderr as the reason.
func runHook(ctx context.Context, h types.HookConfig, stdin []byte, timeout time.Duration) Decision {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "sh", "-c", h.Command)
	if h.Dir != "" {
		cmd.Dir = h.Dir
	}
	cmd.Env = append(os.Environ(), "WIZ_PLUGIN_ROOT="+h.Dir, "CLAUDE_PLUGIN_ROOT="+h.Dir)
	cmd.Stdin = bytes.NewReader(stdin)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = 2 * time.Second

	err := cmd.Run()

	var dec Decision
	_ = json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &dec)

	if err != nil {
		dec.Block = true
		if dec.Reason == "" {
			if r := strings.TrimSpace(stderr.String()); r != "" {
				dec.Reason = r
			} else {
				dec.Reason = err.Error()
			}
		}
	}
	return dec
}

// CombineToolDecisions reduces PreToolUse decisions: any block / explicit
// approved:false denies (first one wins); otherwise any explicit approved:true
// approves (carrying its adjustment); otherwise undecided.
func CombineToolDecisions(ds []Decision) ToolDecision {
	for _, d := range ds {
		if d.Block || (d.Approved != nil && !*d.Approved) {
			return ToolDecision{Decided: true, Approve: false, Reason: d.Reason}
		}
	}
	res := ToolDecision{}
	for _, d := range ds {
		if d.Approved != nil && *d.Approved {
			res = ToolDecision{Decided: true, Approve: true, Adjustment: d.Adjustment}
		}
	}
	return res
}
