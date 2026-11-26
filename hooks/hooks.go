// Package hooks runs shell-command hooks bound to lifecycle events. Each hook
// receives the event payload as JSON on stdin and may return a JSON Decision on
// stdout; PreToolUse decisions gate tool execution.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/mudler/nib/internal/proc"
	"github.com/mudler/nib/plugin/claudeplugin"
	"github.com/mudler/nib/types"
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

	// Context is text a Claude-format hook adds to the model context: its
	// additionalContext, or its plain stdout on SessionStart and
	// UserPromptSubmit. Never read from a nib-format hook.
	Context string `json:"-"`
	// SystemMessage is a Claude-format hook's warning for the user.
	SystemMessage string `json:"-"`
	// Ask asks the user about a PreToolUse call even when the approval
	// mode or a grant would approve it. Claude-format hooks only.
	Ask bool `json:"-"`
	// Claude is true for a decision of a Claude-format hook.
	Claude bool `json:"-"`
}

// ToolDecision is the combined PreToolUse verdict.
type ToolDecision struct {
	Decided    bool
	Approve    bool
	Adjustment string
	Reason     string
	// Ask is set when no hook blocked and one asked for the user's
	// decision; Decided is then false.
	Ask bool
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
	var claudeData []byte
	var out []Decision
	for _, h := range d.hooks {
		if Event(h.Event) != event {
			continue
		}
		if h.Format == types.HookFormatClaude {
			if !claudeplugin.MatchHook(h.Matcher, name) {
				continue
			}
			if claudeData == nil {
				claudeData = claudePayload(event, payload)
			}
			out = append(out, runClaudeHook(ctx, h, event, claudeData))
			continue
		}
		if !matchHook(h.Matcher, name) {
			continue
		}
		timeout := d.timeout
		if h.Timeout > 0 {
			timeout = time.Duration(h.Timeout) * time.Second
		}
		out = append(out, runHook(ctx, h, data, timeout))
	}
	return out
}

// Contexts returns the non-empty Context of each decision, in order.
func Contexts(ds []Decision) []string {
	var out []string
	for _, d := range ds {
		if c := strings.TrimSpace(d.Context); c != "" {
			out = append(out, c)
		}
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
	stdout, stderr, err := execHook(ctx, h, stdin, timeout)

	var dec Decision
	_ = json.Unmarshal(bytes.TrimSpace(stdout), &dec)

	if err != nil {
		dec.Block = true
		if dec.Reason == "" {
			if r := strings.TrimSpace(string(stderr)); r != "" {
				dec.Reason = r
			} else {
				dec.Reason = err.Error()
			}
		}
	}
	return dec
}

// execHook runs a hook command with stdin and returns its output. err is nil
// for a clean exit, an *exec.ExitError for a non-zero status, and another
// error for a timeout or a start failure.
func execHook(ctx context.Context, h types.HookConfig, stdin []byte, timeout time.Duration) (stdoutB, stderrB []byte, err error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "sh", "-c", h.Command)
	// A Claude-format hook runs in the project directory, as in Claude Code;
	// a nib hook runs in its plugin directory.
	if h.Dir != "" && h.Format != types.HookFormatClaude {
		cmd.Dir = h.Dir
	}
	cmd.Env = append(os.Environ(), "NIB_PLUGIN_ROOT="+h.Dir, "CLAUDE_PLUGIN_ROOT="+h.Dir)
	cmd.Env = append(cmd.Env, h.Env...)
	cmd.Stdin = bytes.NewReader(stdin)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Run the hook in its own process group so a timeout kills whatever it
	// spawned. Without this, cancelling reaches only the shell: a hook that
	// backgrounded a child left that child running forever, still holding the
	// output pipes that made the timeout slow in the first place.
	proc.Group(cmd)
	// The backstop for what a group kill cannot reach — a hook that daemonizes
	// with setsid() escapes the group and could otherwise hold Run open
	// indefinitely, stalling the tool call this hook is gating.
	cmd.WaitDelay = 2 * time.Second

	err = cmd.Run()
	if waitDelayOnSuccess(cmd, err) {
		// The hook itself exited cleanly; WaitDelay fired only because
		// something it spawned still held the inherited output pipes open.
		// Treating that as a failure would Block the user's tool call on a hook
		// that approved it — the worst outcome for a mechanism whose whole job
		// is deciding allow or deny.
		err = nil
	}
	if err != nil && cctx.Err() != nil {
		err = fmt.Errorf("hook timed out after %s: %w", timeout, cctx.Err())
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

// waitDelayOnSuccess reports whether err is the WaitDelay backstop firing on a
// hook that itself exited successfully — i.e. only a lingering child kept the
// output pipes open. exec.ErrWaitDelay is not an *exec.ExitError, so without
// this check it falls through to the generic "any error blocks" branch.
func waitDelayOnSuccess(cmd *exec.Cmd, err error) bool {
	return errors.Is(err, exec.ErrWaitDelay) &&
		cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 0
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
	for _, d := range ds {
		if d.Ask {
			return ToolDecision{Ask: true, Reason: d.Reason}
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
