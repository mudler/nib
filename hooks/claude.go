package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/mudler/nib/plugin/claudeplugin"
	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
)

// ClaudeTools maps Claude Code tool names to nib's built-in tools. Claude
// tools that nib lacks (Task, Agent, TodoWrite, NotebookEdit...) are absent,
// so ToolMap.Map reports them as unmapped.
var ClaudeTools = claudeplugin.ToolMap{
	"Bash":       "bash",
	"BashOutput": "bash_job_output",
	"KillShell":  "bash_job_kill",
	"Read":       "read",
	"Write":      "write",
	"Edit":       "edit",
	"MultiEdit":  "edit",
	"Glob":       "glob",
	"LS":         "glob",
	"Grep":       "grep",
	"WebFetch":   "web_fetch",
	"WebSearch":  "web_search",
	"Skill":      "load_skill",
}

var claudeNames = ClaudeTools.Reverse()

// claudeInputKeys renames nib tool argument keys to the Claude Code tool
// input keys that hook scripts read (tool_input.command, .file_path...).
var claudeInputKeys = map[string]map[string]string{
	"bash":  {"script": "command"},
	"read":  {"path": "file_path"},
	"write": {"path": "file_path"},
	"edit":  {"path": "file_path", "old": "old_string", "new": "new_string", "all": "replace_all"},
	"glob":  {"pat": "pattern"},
	"grep":  {"pat": "pattern"},
}

// TranslateMatcher rewrites a Claude Code matcher of a tool event so it
// matches nib's tool names: "Bash" becomes "bash" and "Edit|Write" becomes
// "edit|write". "*" becomes "". A regular expression is returned unchanged.
// Names nib does not know are kept, so they never match.
func TranslateMatcher(matcher string) string {
	m := strings.TrimSpace(matcher)
	if m == "*" {
		return ""
	}
	alts := claudeplugin.SplitMatcher(m)
	if alts == nil {
		return m
	}
	seen := map[string]bool{}
	var out []string
	for _, a := range alts {
		if n, ok := ClaudeTools[a]; ok {
			a = n
		}
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return strings.Join(out, "|")
}

// claudePayload builds the Claude Code hook stdin from nib's event payload.
func claudePayload(event Event, payload any) []byte {
	in, _ := payload.(map[string]any)
	cwd, _ := os.Getwd()
	out := map[string]any{
		"hook_event_name": string(event),
		"cwd":             cwd,
		"session_id":      str(in["session_id"]),
		"transcript_path": "",
		"permission_mode": "default",
	}
	if id := str(in["agent_id"]); id != "" {
		out["agent_id"] = id
	}
	switch event {
	case EventPreToolUse, EventPostToolUse:
		tool := str(in["tool"])
		name := tool
		if c, ok := claudeNames[tool]; ok {
			name = c
		}
		out["tool_name"] = name
		out["tool_input"] = claudeInput(tool, in["arguments"])
		if event == EventPostToolUse {
			out["tool_response"] = in["result"]
		}
	case EventUserPromptSubmit:
		out["prompt"] = str(in["prompt"])
	case EventSessionStart:
		src := str(in["source"])
		if src == "" {
			src = "startup"
		}
		out["source"] = src
	case EventStop:
		out["stop_hook_active"] = false
	}
	data, _ := json.Marshal(out)
	return data
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// claudeInput decodes nib tool arguments (a JSON string or a map) and renames
// the keys a Claude Code hook expects.
func claudeInput(tool string, args any) map[string]any {
	var m map[string]any
	switch a := args.(type) {
	case string:
		_ = json.Unmarshal([]byte(a), &m)
	case map[string]any:
		m = a
	}
	if m == nil {
		return map[string]any{}
	}
	ren := claudeInputKeys[tool]
	out := make(map[string]any, len(m))
	for k, v := range m {
		if r, ok := ren[k]; ok {
			k = r
		}
		out[k] = v
	}
	return out
}

// runClaudeHook runs a hook with the Claude Code contract, parsed by
// claudeplugin.ParseOutput: exit 2 blocks with stderr as the reason; another
// non-zero exit or a timeout is logged and does not block; on exit 0 the JSON
// output gives the decision and additionalContext, and plain stdout of
// SessionStart and UserPromptSubmit is context.
func runClaudeHook(ctx context.Context, h types.HookConfig, event Event, stdin []byte) Decision {
	timeout := claudeplugin.HookHandler{Type: claudeplugin.HookTypeCommand, Timeout: h.Timeout}.TimeoutFor(string(event))
	stdout, stderr, err := execHook(ctx, h, stdin, timeout)
	r := claudeplugin.HookResult{Stdout: stdout, Stderr: stderr}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			r.ExitCode = exit.ExitCode()
		} else {
			r.TimedOut = true
		}
	}
	o, _ := claudeplugin.ParseOutput(string(event), r)
	if o.Error != "" {
		// The command is not logged: an exec-form command may hold option
		// values.
		xlog.Warn("hook failed", "event", string(event), "plugin", h.Dir, "error", o.Error)
	}
	d := Decision{Claude: true, Block: o.Block, Reason: o.Reason, Context: o.AdditionalContext, SystemMessage: o.SystemMessage}
	if event == EventPreToolUse && !o.Block {
		switch o.PermissionDecision {
		case claudeplugin.PermissionAllow:
			if o.UpdatedInput != nil {
				// nib cannot rewrite the tool input yet: approving the
				// original input would run what the hook meant to change,
				// so the user is asked instead.
				d.Ask = true
				d.Reason = "a hook wanted to change the tool input, which nib does not support"
				break
			}
			d.Approved = boolPtr(true)
		case claudeplugin.PermissionAsk:
			d.Ask = true
		}
	}
	return d
}

func boolPtr(b bool) *bool { return &b }
