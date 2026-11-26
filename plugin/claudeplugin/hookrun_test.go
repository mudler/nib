package claudeplugin

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestBuildInput(t *testing.T) {
	data, err := BuildInput("PreToolUse", CommonInput{SessionID: "s1", Cwd: "/proj", PermissionMode: "default"},
		map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "ls"}})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(data, &got)
	if got["hook_event_name"] != "PreToolUse" || got["session_id"] != "s1" || got["cwd"] != "/proj" || got["tool_name"] != "Bash" {
		t.Errorf("input = %v", got)
	}
	if _, ok := got["agent_id"]; ok {
		t.Error("empty agent_id is omitted")
	}
	if _, err := BuildInput("Stop", CommonInput{}, []int{1}); err == nil {
		t.Error("specific must be an object")
	}
}

func TestParseOutput(t *testing.T) {
	tests := []struct {
		name  string
		event string
		r     HookResult
		want  Outcome
	}{
		{"exit 2", "PreToolUse", HookResult{ExitCode: 2, Stderr: []byte("no rm\n")}, Outcome{Continue: true, Block: true, Reason: "no rm"}},
		{"exit 1", "PreToolUse", HookResult{ExitCode: 1, Stderr: []byte("boom")}, Outcome{Continue: true, Error: "hook exited with status 1: boom"}},
		{"timeout", "Stop", HookResult{TimedOut: true}, Outcome{Continue: true, Error: "hook timed out"}},
		{"plain context", "SessionStart", HookResult{Stdout: []byte("hello\n")}, Outcome{Continue: true, AdditionalContext: "hello"}},
		{"plain ignored", "PostToolUse", HookResult{Stdout: []byte("hello")}, Outcome{Continue: true}},
		{"deny", "PreToolUse", HookResult{Stdout: []byte(`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"protected"}}`)},
			Outcome{Continue: true, Block: true, PermissionDecision: "deny", Reason: "protected"}},
		{"allow with input", "PreToolUse", HookResult{Stdout: []byte(`{"hookSpecificOutput":{"permissionDecision":"allow","updatedInput":{"command":"ls -l"}}}`)},
			Outcome{Continue: true, PermissionDecision: "allow", UpdatedInput: map[string]any{"command": "ls -l"}}},
		{"context json", "UserPromptSubmit", HookResult{Stdout: []byte(`{"hookSpecificOutput":{"additionalContext":"ctx"},"systemMessage":"note","suppressOutput":true}`)},
			Outcome{Continue: true, AdditionalContext: "ctx", SystemMessage: "note", SuppressOutput: true}},
		{"stop", "Stop", HookResult{Stdout: []byte(`{"continue":false,"stopReason":"done","decision":"block","reason":"keep going"}`)},
			Outcome{Continue: false, StopReason: "done", Block: true, Reason: "keep going"}},
		{"legacy approve", "PreToolUse", HookResult{Stdout: []byte(`{"decision":"approve"}`)}, Outcome{Continue: true, PermissionDecision: "allow"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseOutput(tt.event, tt.r)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
	o, err := ParseOutput("SessionStart", HookResult{Stdout: []byte("{not json")})
	if err == nil || o.Error == "" || o.AdditionalContext != "{not json" {
		t.Errorf("bad JSON: %+v %v", o, err)
	}
}

func TestCombine(t *testing.T) {
	c := Combine("PreToolUse", []Outcome{
		{Continue: true, PermissionDecision: "allow", UpdatedInput: map[string]any{"a": 1}, AdditionalContext: "one"},
		{Continue: true, PermissionDecision: "ask", Reason: "check", AdditionalContext: "two"},
		{Continue: true, Error: "e1"},
	})
	if c.Block || c.PermissionDecision != "ask" || c.Reason != "check" || c.UpdatedInput == nil || c.AdditionalContext != "one\n\ntwo" || c.Error != "e1" {
		t.Errorf("combined = %+v", c)
	}
	c = Combine("PreToolUse", []Outcome{
		{Continue: true, PermissionDecision: "allow"},
		{Continue: true, Block: true, PermissionDecision: "deny", Reason: "first"},
		{Continue: true, Block: true, Reason: "second"},
		{Continue: false, StopReason: "stop"},
	})
	if !c.Block || c.Reason != "first" || c.PermissionDecision != "deny" || c.Continue || c.StopReason != "stop" {
		t.Errorf("combined = %+v", c)
	}
	if c := Combine("Stop", nil); !c.Continue || c.Block {
		t.Errorf("empty = %+v", c)
	}
}

func TestRunHooks(t *testing.T) {
	groups := []HookGroup{
		{Event: "PreToolUse", Matcher: "Bash", Handlers: []HookHandler{
			{Type: HookTypeCommand, Command: "check.sh"},
			{Type: HookTypeHTTP, URL: "https://x"},
			{Type: HookTypeCommand, Command: "fail.sh", OnFailure: "block"},
		}},
		{Event: "PreToolUse", Matcher: "Edit|Write", Handlers: []HookHandler{{Type: HookTypeCommand, Command: "never.sh"}}},
		{Event: "Stop", Handlers: []HookHandler{{Type: HookTypeCommand, Command: "stop.sh"}}},
	}
	var ran []HookCommand
	ex := ExecutorFunc(func(_ context.Context, c HookCommand) (HookResult, error) {
		ran = append(ran, c)
		switch c.Command {
		case "/root/check.sh":
			return HookResult{Stdout: []byte(`{"hookSpecificOutput":{"additionalContext":"checked"}}`)}, nil
		case "/root/fail.sh":
			return HookResult{ExitCode: 1, Stderr: []byte("crashed")}, nil
		}
		return HookResult{}, errors.New("unexpected")
	})
	prepare := func(h HookHandler) HookCommand {
		return HookCommand{Command: "/root/" + h.Command, Env: []string{"CLAUDE_PLUGIN_ROOT=/root"}}
	}
	out, skipped := RunHooks(context.Background(), ex, groups, "PreToolUse", "Bash", []byte(`{}`), prepare)
	if len(ran) != 2 || ran[0].Timeout != 600*time.Second || string(ran[0].Stdin) != "{}" || ran[0].Event != "PreToolUse" {
		t.Fatalf("ran = %+v", ran)
	}
	if len(skipped) != 1 || skipped[0].Type != HookTypeHTTP {
		t.Errorf("skipped = %+v", skipped)
	}
	if !out.Block || out.AdditionalContext != "checked" {
		t.Errorf("onFailure block: %+v", out)
	}
	if got := MatchingHandlers(groups, "Stop", "anything"); len(got) != 1 {
		t.Errorf("Stop ignores matchers: %+v", got)
	}
}
