package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

// A bash call that names its script with the command alias runs that script
// (mcp.BashScript), so every check made before the call must judge the same
// script. A check that read only "script" would see an empty script for such a
// call.
func TestCommandAliasJudgedLikeScript(t *testing.T) {
	cmds := newReadOnlyCommands(nil)
	for _, script := range []string{"ls -la", "git status", "rm -rf build", "go test ./... && git push"} {
		viaScript := `{"script":"` + script + `"}`
		viaCommand := `{"command":"` + script + `"}`

		p1, ok1 := BashGrantPrefix(viaScript)
		p2, ok2 := BashGrantPrefix(viaCommand)
		if p1 != p2 || ok1 != ok2 {
			t.Errorf("%q: BashGrantPrefix script=(%q,%v) command=(%q,%v)", script, p1, ok1, p2, ok2)
		}
		s1, g1 := GrantScope("bash", viaScript)
		s2, g2 := GrantScope("bash", viaCommand)
		if s1 != s2 || g1 != g2 {
			t.Errorf("%q: GrantScope script=(%q,%q) command=(%q,%q)", script, s1, g1, s2, g2)
		}
		for _, tool := range []string{"bash", "bash_background"} {
			if a, b := IsReadOnly(tool, viaScript, cmds), IsReadOnly(tool, viaCommand, cmds); a != b {
				t.Errorf("%q: IsReadOnly(%s) script=%v command=%v", script, tool, a, b)
			}
			if a, b := FormatToolCall(tool, viaScript), FormatToolCall(tool, viaCommand); a != b {
				t.Errorf("%q: FormatToolCall(%s) script=%q command=%q", script, tool, a, b)
			}
		}
	}
}

// Two different scripts in one call are refused by the handler, so no check
// may treat such a call as safe.
func TestConflictingScriptsAreNeverSafe(t *testing.T) {
	args := `{"script":"ls","command":"rm -rf /"}`
	if _, ok := BashGrantPrefix(args); ok {
		t.Error("BashGrantPrefix derived a prefix from a call with two scripts")
	}
	if IsReadOnly("bash", args, newReadOnlyCommands(nil)) {
		t.Error("IsReadOnly approved a call with two scripts")
	}
}

// decideToolCall rewrites a command-alias call to {"script": ...} before any
// check, so a PreToolUse hook and the approval prompt read the documented key.
// A user's hook that inspects only "script" is not bypassed by the alias.
func TestDecideToolCallRewritesCommandAlias(t *testing.T) {
	var seen string
	s := newDecideSession(types.ApprovalPrompt, func(req ToolCallRequest) ToolCallResponse {
		seen = req.Arguments
		return ToolCallResponse{Approved: false}
	})
	s.decideToolCall(ToolCallRequest{Name: "bash", Arguments: `{"command":"rm -rf build","timeout":5}`})
	if seen != `{"script":"rm -rf build","timeout":5}` {
		t.Fatalf("approval prompt got %s, want the script form", seen)
	}
}

func TestApproverStateUsesCommandAlias(t *testing.T) {
	f := verdictFor("inspect", 1)
	req := ToolCallRequest{Name: "bash", Arguments: `{"command":"go test ./chat/"}`}
	NewApprover(f, types.AutoApproveConfig{}, "/work/repo").Judge(context.Background(), req)
	if len(f.states) != 1 || !strings.Contains(f.states[0], "command: go test ./chat/") {
		t.Fatalf("classifier state = %q, want the aliased script", f.states)
	}
}
