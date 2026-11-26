package hooks

import (
	"bytes"
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

// claudeOutput is the JSON a Claude-format hook may print on exit 0.
type claudeOutput struct {
	Continue      *bool  `json:"continue"`
	StopReason    string `json:"stopReason"`
	SystemMessage string `json:"systemMessage"`
	Decision      string `json:"decision"`
	Reason        string `json:"reason"`
	HookSpecific  *struct {
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
		AdditionalContext        string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// runClaudeHook runs a hook with the Claude Code contract:
//
//   - exit 2 blocks, with stderr as the reason;
//   - another non-zero exit, a timeout or a start failure is logged and does
//     not block;
//   - on exit 0, a JSON object is read for decision, permissionDecision and
//     additionalContext; plain stdout of SessionStart and UserPromptSubmit is
//     context.
func runClaudeHook(ctx context.Context, h types.HookConfig, event Event, stdin []byte) Decision {
	timeout := claudeplugin.HookHandler{Type: claudeplugin.HookCommand, Timeout: h.Timeout}.TimeoutFor(string(event))
	stdout, stderr, err := execHook(ctx, h, stdin, timeout)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 2 {
			reason := strings.TrimSpace(string(stderr))
			if reason == "" {
				reason = "blocked by hook"
			}
			return Decision{Block: true, Reason: reason}
		}
		xlog.Warn("hook failed", "event", string(event), "command", h.Command, "error", err, "stderr", strings.TrimSpace(string(stderr)))
		return Decision{}
	}
	text := bytes.TrimSpace(stdout)
	if len(text) == 0 {
		return Decision{}
	}
	if text[0] == '{' {
		var o claudeOutput
		if json.Unmarshal(text, &o) == nil {
			return claudeDecision(event, o)
		}
	}
	if event == EventSessionStart || event == EventUserPromptSubmit {
		return Decision{Context: string(text)}
	}
	return Decision{}
}

func claudeDecision(event Event, o claudeOutput) Decision {
	d := Decision{SystemMessage: o.SystemMessage, Reason: o.Reason}
	if hs := o.HookSpecific; hs != nil {
		d.Context = hs.AdditionalContext
		if hs.PermissionDecisionReason != "" {
			d.Reason = hs.PermissionDecisionReason
		}
		if event == EventPreToolUse {
			switch hs.PermissionDecision {
			case "allow":
				d.Approved = boolPtr(true)
			case "deny":
				d.Block = true
			}
		}
	}
	switch o.Decision {
	case "block":
		d.Block = true
	case "approve":
		if event == EventPreToolUse && !d.Block {
			d.Approved = boolPtr(true)
		}
	}
	if d.Block && d.Reason == "" {
		d.Reason = "blocked by hook"
	}
	return d
}

func boolPtr(b bool) *bool { return &b }
