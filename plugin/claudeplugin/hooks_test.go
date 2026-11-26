package claudeplugin

import (
	"testing"
	"time"
)

func TestMatchHook(t *testing.T) {
	tests := []struct {
		matcher, value string
		want           bool
	}{
		{"", "Bash", true},
		{"*", "Bash", true},
		{"Bash", "Bash", true},
		{"Bash", "BashOutput", false},
		{"Edit|Write", "Write", true},
		{"Edit|Write", "MultiEdit", false},
		{"Edit, Write", "Edit", true},
		{"startup|clear|compact", "resume", false},
		{"mcp__memory__.*", "mcp__memory__store", true},
		{".*", "Anything", true},
		{"^Notebook", "NotebookEdit", true},
		{"(", "(", false},
	}
	for _, tt := range tests {
		if got := MatchHook(tt.matcher, tt.value); got != tt.want {
			t.Errorf("MatchHook(%q, %q) = %v, want %v", tt.matcher, tt.value, got, tt.want)
		}
	}
}

func TestHookTimeout(t *testing.T) {
	tests := []struct {
		h     HookHandler
		event string
		want  time.Duration
	}{
		{HookHandler{Type: HookCommand}, "PreToolUse", 600 * time.Second},
		{HookHandler{Type: HookCommand}, "UserPromptSubmit", 30 * time.Second},
		{HookHandler{Type: HookCommand, Timeout: 5}, "PreToolUse", 5 * time.Second},
		{HookHandler{Type: HookPrompt}, "Stop", 30 * time.Second},
		{HookHandler{Type: HookAgent}, "Stop", 60 * time.Second},
		{HookHandler{Type: HookHTTP}, "MessageDisplay", 10 * time.Second},
	}
	for _, tt := range tests {
		if got := tt.h.TimeoutFor(tt.event); got != tt.want {
			t.Errorf("%+v on %s: got %v, want %v", tt.h, tt.event, got, tt.want)
		}
	}
}

func TestLoadHooksShapes(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".claude-plugin/plugin.json": `{"name":"hk","hooks":["./extra/hooks.json",{"PostToolUse":[{"matcher":"Write|Edit","hooks":[{"type":"command","command":"fmt.sh"}]}]}]}`,
		"hooks/hooks.json": `{"description":"x","modules":["./m.js"],"hooks":{
			"SessionStart":[{"matcher":"startup|clear|compact","hooks":[{"type":"command","command":"\"${CLAUDE_PLUGIN_ROOT}/hooks/run\" start","async":false,"shell":"bash"}]}],
			"Stop":[{"matcher":"ignored","hooks":[{"type":"prompt","prompt":"Done? $ARGUMENTS","timeout":20}]}],
			"MadeUpEvent":[{"hooks":[{"type":"command","command":"x"}]}],
			"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"http","url":"https://hook.example/check","headers":{"A":"b"}},{"type":"mcp_tool","server":"s","tool":"t","input":{"p":"${tool_input.file_path}"}},{"type":"bogus"}]},{"command":"cursor-style"}]
		}}`,
		"extra/hooks.json": `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"remind","timeout":10}]}]}}`,
	})
	p, err := Load(root, LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	byEvent := map[string][]HookGroup{}
	for _, g := range p.Hooks {
		byEvent[g.Event] = append(byEvent[g.Event], g)
	}
	if g := byEvent["SessionStart"]; len(g) != 1 || g[0].Matcher != "startup|clear|compact" || g[0].Handlers[0].Shell != "bash" {
		t.Errorf("SessionStart = %+v", g)
	}
	if g := byEvent["Stop"]; len(g) != 1 || g[0].Matcher != "" || g[0].Handlers[0].Type != HookPrompt || g[0].Handlers[0].Timeout != 20 {
		t.Errorf("Stop = %+v (matcher must be dropped for Stop)", g)
	}
	if g := byEvent["PreToolUse"]; len(g) != 1 || len(g[0].Handlers) != 2 || g[0].Handlers[1].Input["p"] != "${tool_input.file_path}" {
		t.Errorf("PreToolUse = %+v", g)
	}
	if g := byEvent["PostToolUse"]; len(g) != 1 || g[0].Source != "plugin.json" {
		t.Errorf("PostToolUse = %+v", g)
	}
	if g := byEvent["UserPromptSubmit"]; len(g) != 1 || g[0].Source != "extra/hooks.json" {
		t.Errorf("UserPromptSubmit = %+v", g)
	}
	if len(byEvent["MadeUpEvent"]) != 1 {
		t.Errorf("unknown events are kept as data")
	}
	for _, want := range []string{"modules", "unknown hook event", "bogus", `no "hooks" list`} {
		if !hasSkip(p, "hook", want) {
			t.Errorf("missing skip %q in %+v", want, p.Skipped)
		}
	}
	if !p.RunsCode() {
		t.Error("command hooks run code")
	}
}
