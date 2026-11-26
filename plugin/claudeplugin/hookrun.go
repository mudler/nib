package claudeplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// This file implements the hook contract of the Claude Code plugin format
// with an executor the host supplies, so a host can run hook commands in its
// own sandbox. The package itself never starts a process.

// HookCommand is one command handler to run.
type HookCommand struct {
	Event   string
	Handler HookHandler
	// Command is the shell command (shell form). With Args set, Command is
	// the program and Args its arguments (exec form, no shell).
	Command string
	Args    []string
	// Env holds extra KEY=VALUE entries: CLAUDE_PLUGIN_ROOT,
	// CLAUDE_PLUGIN_DATA, CLAUDE_PROJECT_DIR and the options from
	// [OptionEnv]. The host decides what else the process inherits.
	Env []string
	// Dir is the working directory, normally the project directory.
	Dir string
	// Stdin is the JSON built by [BuildInput].
	Stdin   []byte
	Timeout time.Duration
}

// HookResult is what an executor observed.
type HookResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
	// TimedOut is true when the executor stopped the command at Timeout.
	TimedOut bool
}

// Executor runs a hook command. It returns an error only when the command
// could not be started; a non-zero exit is a HookResult.
type Executor interface {
	Run(ctx context.Context, c HookCommand) (HookResult, error)
}

// ExecutorFunc adapts a function to [Executor].
type ExecutorFunc func(ctx context.Context, c HookCommand) (HookResult, error)

// Run calls f.
func (f ExecutorFunc) Run(ctx context.Context, c HookCommand) (HookResult, error) { return f(ctx, c) }

// CommonInput holds the stdin fields every hook event receives.
type CommonInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	PermissionMode string `json:"permission_mode,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	AgentType      string `json:"agent_type,omitempty"`
}

// BuildInput returns the stdin JSON of a hook: the common fields,
// hook_event_name, and the event-specific fields of specific (a struct or
// a map that marshals to a JSON object, or nil), such as tool_name,
// tool_input, tool_response, prompt or source. Specific fields win over
// common ones of the same name.
func BuildInput(event string, common CommonInput, specific any) ([]byte, error) {
	out := map[string]any{}
	if err := mergeJSON(out, common); err != nil {
		return nil, err
	}
	out["hook_event_name"] = event
	if specific != nil {
		if err := mergeJSON(out, specific); err != nil {
			return nil, fmt.Errorf("hook input for %s: %w", event, err)
		}
	}
	return json.Marshal(out)
}

func mergeJSON(dst map[string]any, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("must marshal to a JSON object: %w", err)
	}
	for k, val := range m {
		dst[k] = val
	}
	return nil
}

// Permission decisions of a PreToolUse hook.
const (
	PermissionAllow = "allow"
	PermissionDeny  = "deny"
	PermissionAsk   = "ask"
)

// Outcome is the parsed result of one hook, or of several combined.
type Outcome struct {
	// Block stops the action: exit status 2, "decision": "block", or a
	// PreToolUse "deny".
	Block  bool
	Reason string
	// PermissionDecision is "allow", "deny", "ask" or "" (PreToolUse).
	PermissionDecision string
	// UpdatedInput replaces the tool input (PreToolUse allow).
	UpdatedInput map[string]any
	// AdditionalContext is text for the model: hookSpecificOutput
	// additionalContext, or plain stdout on the events that take it.
	AdditionalContext string
	SystemMessage     string
	// Continue is false when a hook asked to stop the whole session turn.
	Continue       bool
	StopReason     string
	SuppressOutput bool
	// Error describes a non-blocking failure: another non-zero exit, a
	// timeout, or JSON that does not parse. The action continues.
	Error string
}

// nonBlockingEvents cannot be blocked: exit status 2 only reports stderr.
var nonBlockingEvents = map[string]bool{
	"SessionStart": true, "SessionEnd": true, "Setup": true, "Notification": true,
	"SubagentStart": true, "PostCompact": true, "CwdChanged": true, "FileChanged": true,
	"InstructionsLoaded": true, "StopFailure": true, "WorktreeRemove": true,
	"PostModelSwitch": true, "MessageDisplay": true, "DirectoryAdded": true,
}

// plainContextEvents take plain stdout of an exit-0 hook as context.
var plainContextEvents = map[string]bool{
	"SessionStart": true, "UserPromptSubmit": true, "UserPromptExpansion": true, "PostModelSwitch": true,
}

type hookOutput struct {
	Continue       *bool  `json:"continue"`
	StopReason     string `json:"stopReason"`
	SuppressOutput bool   `json:"suppressOutput"`
	SystemMessage  string `json:"systemMessage"`
	Decision       string `json:"decision"`
	Reason         string `json:"reason"`
	HookSpecific   *struct {
		PermissionDecision       string         `json:"permissionDecision"`
		PermissionDecisionReason string         `json:"permissionDecisionReason"`
		UpdatedInput             map[string]any `json:"updatedInput"`
		AdditionalContext        string         `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// ParseOutput reads a hook result with the format's rules:
//
//   - exit 2 blocks, with stderr as the reason, on events that can be
//     blocked (on SessionStart and the other notification events it is an
//     Error);
//   - another non-zero exit or a timeout is a non-blocking Error;
//   - on exit 0, a JSON object on stdout gives continue, stopReason,
//     systemMessage, decision, reason and hookSpecificOutput
//     (permissionDecision, permissionDecisionReason, updatedInput,
//     additionalContext); other stdout is context on SessionStart,
//     UserPromptSubmit, UserPromptExpansion and PostModelSwitch, and is
//     ignored elsewhere.
//
// The error return is reserved for JSON that looks like an object but does
// not parse; the Outcome then carries the same text in Error.
func ParseOutput(event string, r HookResult) (Outcome, error) {
	o := Outcome{Continue: true}
	switch {
	case r.TimedOut:
		o.Error = "hook timed out"
		return o, nil
	case r.ExitCode == 2 && nonBlockingEvents[event]:
		o.Error = "hook exited with status 2: " + strings.TrimSpace(string(r.Stderr))
		return o, nil
	case r.ExitCode == 2:
		o.Block = true
		o.Reason = strings.TrimSpace(string(r.Stderr))
		if o.Reason == "" {
			o.Reason = "blocked by hook"
		}
		return o, nil
	case r.ExitCode != 0:
		o.Error = fmt.Sprintf("hook exited with status %d: %s", r.ExitCode, strings.TrimSpace(string(r.Stderr)))
		return o, nil
	}
	text := bytes.TrimSpace(r.Stdout)
	if len(text) == 0 {
		return o, nil
	}
	if text[0] != '{' {
		if plainContextEvents[event] {
			o.AdditionalContext = string(text)
		}
		return o, nil
	}
	var h hookOutput
	if err := json.Unmarshal(text, &h); err != nil {
		o.Error = "hook output is not valid JSON: " + err.Error()
		if plainContextEvents[event] {
			o.AdditionalContext = string(text)
		}
		return o, fmt.Errorf("%s", o.Error)
	}
	if h.Continue != nil {
		o.Continue = *h.Continue
	}
	o.StopReason = h.StopReason
	o.SuppressOutput = h.SuppressOutput
	o.SystemMessage = h.SystemMessage
	o.Reason = h.Reason
	switch h.Decision {
	case "block":
		o.Block = true
	case "approve":
		if event == "PreToolUse" {
			o.PermissionDecision = PermissionAllow
		}
	}
	if hs := h.HookSpecific; hs != nil {
		o.AdditionalContext = hs.AdditionalContext
		if hs.PermissionDecisionReason != "" {
			o.Reason = hs.PermissionDecisionReason
		}
		switch hs.PermissionDecision {
		case PermissionAllow, PermissionAsk:
			o.PermissionDecision = hs.PermissionDecision
		case PermissionDeny:
			o.PermissionDecision = PermissionDeny
			o.Block = true
		}
		o.UpdatedInput = hs.UpdatedInput
	}
	if o.Block && o.Reason == "" {
		o.Reason = "blocked by hook"
	}
	return o, nil
}

// Combine merges the outcomes of the handlers that ran for one event: any
// block blocks (the first reason wins); the permission decision is deny,
// then ask, then allow; the last updatedInput wins; context, system
// messages and errors are joined; any continue:false stops.
func Combine(event string, outs []Outcome) Outcome {
	c := Outcome{Continue: true}
	rank := map[string]int{"": 0, PermissionAllow: 1, PermissionAsk: 2, PermissionDeny: 3}
	var ctx, msgs, errs []string
	for _, o := range outs {
		if o.Block && !c.Block {
			c.Block = true
			c.Reason = o.Reason
		}
		if rank[o.PermissionDecision] > rank[c.PermissionDecision] {
			c.PermissionDecision = o.PermissionDecision
			if !c.Block && o.Reason != "" {
				c.Reason = o.Reason
			}
		}
		if o.UpdatedInput != nil {
			c.UpdatedInput = o.UpdatedInput
		}
		if !o.Continue && c.Continue {
			c.Continue = false
			c.StopReason = o.StopReason
		}
		c.SuppressOutput = c.SuppressOutput || o.SuppressOutput
		if s := strings.TrimSpace(o.AdditionalContext); s != "" {
			ctx = append(ctx, s)
		}
		if o.SystemMessage != "" {
			msgs = append(msgs, o.SystemMessage)
		}
		if o.Error != "" {
			errs = append(errs, o.Error)
		}
	}
	if c.Block || c.PermissionDecision == PermissionDeny {
		c.UpdatedInput = nil // a denied call runs no input
	}
	c.AdditionalContext = strings.Join(ctx, "\n\n")
	c.SystemMessage = strings.Join(msgs, "\n")
	c.Error = strings.Join(errs, "\n")
	return c
}

// MatchingHandlers returns the handlers of event whose group matcher
// matches value (a tool name for tool events, a source or reason for
// others), in order. Events that ignore matchers match every group.
func MatchingHandlers(groups []HookGroup, event, value string) []HookHandler {
	var out []HookHandler
	for _, g := range groups {
		if g.Event != event {
			continue
		}
		if noMatcherEvents[event] || MatchHook(g.Matcher, value) {
			out = append(out, g.Handlers...)
		}
	}
	return out
}

// RunHooks runs the command handlers of event that match value through ex,
// all with the same stdin, and returns the combined outcome. prepare fills
// in a HookCommand for a handler (command expansion, environment, working
// directory); Event, Handler, Stdin and Timeout are set by RunHooks when
// prepare leaves them empty. Handlers of other types are skipped and
// returned in skipped, for the host to run or report.
func RunHooks(ctx context.Context, ex Executor, groups []HookGroup, event, value string, stdin []byte, prepare func(HookHandler) HookCommand) (combined Outcome, skipped []HookHandler) {
	var outs []Outcome
	for _, h := range MatchingHandlers(groups, event, value) {
		if h.Type != HookTypeCommand {
			skipped = append(skipped, h)
			continue
		}
		c := HookCommand{Command: h.Command, Args: h.Args}
		if prepare != nil {
			c = prepare(h)
		}
		c.Event, c.Handler = event, h
		if c.Stdin == nil {
			c.Stdin = stdin
		}
		if c.Timeout == 0 {
			c.Timeout = h.TimeoutFor(event)
		}
		r, err := ex.Run(ctx, c)
		if err != nil {
			outs = append(outs, Outcome{Continue: true, Error: err.Error()})
			continue
		}
		o, _ := ParseOutput(event, r)
		if o.Error != "" && h.OnFailure == "block" {
			o.Block, o.Reason = true, o.Error
		}
		outs = append(outs, o)
	}
	return Combine(event, outs), skipped
}
