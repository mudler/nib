package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/nib/types"
)

func TestTranslateMatcher(t *testing.T) {
	tests := map[string]string{
		"Bash":            "bash",
		"Edit|Write":      "edit|write",
		"Edit|MultiEdit":  "edit",
		"Read, Grep":      "read|grep",
		"*":               "",
		"":                "",
		"Task":            "Task",
		"mcp__memory__.*": "mcp__memory__.*",
	}
	for in, want := range tests {
		if got := TranslateMatcher(in); got != want {
			t.Errorf("TranslateMatcher(%q) = %q, want %q", in, got, want)
		}
	}
}

func claudeHook(t *testing.T, event, matcher, body string) (types.HookConfig, string) {
	t.Helper()
	dir := t.TempDir()
	script := writeScript(t, dir, "h.sh", body)
	return types.HookConfig{Event: event, Matcher: matcher, Command: script, Dir: dir, Format: types.HookFormatClaude}, dir
}

func TestClaudeHookStdinShape(t *testing.T) {
	h, dir := claudeHook(t, "PreToolUse", TranslateMatcher("Bash"), `cat > "$CLAUDE_PLUGIN_ROOT/in.json"`)
	d := New([]types.HookConfig{h})
	if got := d.Fire(context.Background(), EventPreToolUse, "edit", nil); len(got) != 0 {
		t.Fatalf("a Bash matcher must not fire on edit: %+v", got)
	}
	got := d.Fire(context.Background(), EventPreToolUse, "bash", map[string]any{
		"tool": "bash", "arguments": `{"script":"ls -la","timeout":5}`,
	})
	if len(got) != 1 || got[0].Block || got[0].Approved != nil {
		t.Fatalf("decisions = %+v", got)
	}
	data, err := os.ReadFile(filepath.Join(dir, "in.json"))
	if err != nil {
		t.Fatal(err)
	}
	var in map[string]any
	if err := json.Unmarshal(data, &in); err != nil {
		t.Fatal(err)
	}
	if in["hook_event_name"] != "PreToolUse" || in["tool_name"] != "Bash" || in["cwd"] == "" {
		t.Errorf("stdin = %v", in)
	}
	if ti, _ := in["tool_input"].(map[string]any); ti["command"] != "ls -la" {
		t.Errorf("tool_input = %v", in["tool_input"])
	}
}

func TestClaudeHookExitCodes(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		block     bool
		approved  *bool
		reason    string
		timeoutSc int
	}{
		{"exit 2 blocks with stderr", "echo 'rm is not allowed' >&2; exit 2", true, nil, "rm is not allowed", 0},
		{"exit 1 does not block", "echo oops >&2; exit 1", false, nil, "", 0},
		{"deny", `echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"protected file"}}'`, true, nil, "protected file", 0},
		{"allow", `echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}'`, false, boolp(true), "", 0},
		{"ask is undecided", `echo '{"hookSpecificOutput":{"permissionDecision":"ask"}}'`, false, nil, "", 0},
		{"legacy decision block", `echo '{"decision":"block","reason":"no"}'`, true, nil, "no", 0},
		{"timeout does not block", "sleep 5", false, nil, "", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := claudeHook(t, "PreToolUse", "", tt.body)
			h.Timeout = tt.timeoutSc
			got := New([]types.HookConfig{h}).Fire(context.Background(), EventPreToolUse, "bash", map[string]any{"tool": "bash"})
			if len(got) != 1 {
				t.Fatalf("decisions = %+v", got)
			}
			d := got[0]
			if d.Block != tt.block || (d.Approved == nil) != (tt.approved == nil) || (tt.reason != "" && d.Reason != tt.reason) {
				t.Errorf("decision = %+v", d)
			}
			td := CombineToolDecisions(got)
			if tt.block && (!td.Decided || td.Approve) {
				t.Errorf("combined = %+v", td)
			}
		})
	}
}

func TestClaudeHookContext(t *testing.T) {
	start, _ := claudeHook(t, "SessionStart", "startup|clear|compact", `echo "You have superpowers."`)
	resume, _ := claudeHook(t, "SessionStart", "resume", `echo "resume only"`)
	prompt, _ := claudeHook(t, "UserPromptSubmit", "", `echo '{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"Remember the style guide."}}'`)
	stop, _ := claudeHook(t, "Stop", "", `echo "plain stdout on Stop is not context"`)
	d := New([]types.HookConfig{start, resume, prompt, stop})

	ctx := Contexts(d.Fire(context.Background(), EventSessionStart, "startup", map[string]any{"source": "startup"}))
	if len(ctx) != 1 || ctx[0] != "You have superpowers." {
		t.Errorf("SessionStart context = %q", ctx)
	}
	ctx = Contexts(d.Fire(context.Background(), EventUserPromptSubmit, "", map[string]any{"prompt": "hi"}))
	if len(ctx) != 1 || ctx[0] != "Remember the style guide." {
		t.Errorf("UserPromptSubmit context = %q", ctx)
	}
	if ctx = Contexts(d.Fire(context.Background(), EventStop, "", nil)); len(ctx) != 0 {
		t.Errorf("Stop context = %q", ctx)
	}
}

func TestNibHookFormatUnchanged(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "h.sh", "echo 'context?'; exit 1")
	got := New([]types.HookConfig{{Event: "SessionStart", Command: script, Dir: dir}}).Fire(context.Background(), EventSessionStart, "startup", nil)
	if len(got) != 1 || !got[0].Block || got[0].Context != "" {
		t.Errorf("nib-format hooks keep their contract: %+v", got)
	}
}

func TestHookEnvAndTimeoutFields(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "h.sh", `echo "$CLAUDE_PLUGIN_DATA" > "$NIB_PLUGIN_ROOT/data.txt"`)
	h := types.HookConfig{Event: "Stop", Command: script, Dir: dir, Env: []string{"CLAUDE_PLUGIN_DATA=/data/x"}, Timeout: 3}
	New([]types.HookConfig{h}).Fire(context.Background(), EventStop, "", nil)
	if b, _ := os.ReadFile(filepath.Join(dir, "data.txt")); string(b) != "/data/x\n" {
		t.Errorf("env = %q", b)
	}
}
