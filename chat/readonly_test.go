package chat

import "testing"

func TestIsReadOnly(t *testing.T) {
	cmds := newReadOnlyCommands(nil)
	cases := []struct {
		name string
		tool string
		args string
		want bool
	}{
		// read-only built-in tools (args-independent)
		{"read tool", "read", `{"path":"/etc/hosts"}`, true},
		{"grep tool", "grep", `{"pat":"x"}`, true},
		{"glob tool", "glob", `{"pat":"*.go"}`, true},
		{"cron_list", "cron_list", `{}`, true},
		// mutating built-in tools
		{"write tool", "write", `{"path":"x","content":"y"}`, false},
		{"edit tool", "edit", `{"path":"x"}`, false},
		{"spawn_agent", "spawn_agent", `{"agent_type":"r","task":"t"}`, false},
		// unknown / MCP tool
		{"unknown mcp", "some_mcp_tool", `{"q":"1"}`, false},
		// bash whole-command
		{"bash ls", "bash", `{"script":"ls -la"}`, true},
		{"bash cat", "bash", `{"script":"cat f"}`, true},
		{"bash rm", "bash", `{"script":"rm f"}`, false},
		{"bash sed -i", "bash", `{"script":"sed -i s/a/b/ f"}`, false}, // sed excluded
		// bash pairs
		{"bash git status", "bash", `{"script":"git status"}`, true},
		{"bash git push", "bash", `{"script":"git push"}`, false},
		{"bash go list", "bash", `{"script":"go list ./..."}`, true},
		{"bash go build", "bash", `{"script":"go build"}`, false},
		// background bash uses the same gate
		{"bash_background ls", "bash_background", `{"script":"ls"}`, true},
		{"bash_background rm", "bash_background", `{"script":"rm x"}`, false},
		// smuggling — every one must be false
		{"chain", "bash", `{"script":"git status && rm -rf /"}`, false},
		{"pipe to sh", "bash", `{"script":"cat f | sh"}`, false},
		{"semicolon", "bash", `{"script":"ls; rm x"}`, false},
		{"subshell", "bash", `{"script":"echo $(rm x)"}`, false},
		{"redirect", "bash", `{"script":"git status > /etc/passwd"}`, false},
		{"process sub", "bash", `{"script":"cat <(rm x)"}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsReadOnly(c.tool, c.args, cmds); got != c.want {
				t.Errorf("IsReadOnly(%q, %s) = %v, want %v", c.tool, c.args, got, c.want)
			}
		})
	}
}

func TestIsReadOnlyConfigExtension(t *testing.T) {
	cmds := newReadOnlyCommands([]string{"terraform plan"})
	if !IsReadOnly("bash", `{"script":"terraform plan"}`, cmds) {
		t.Error("configured 'terraform plan' should be read-only")
	}
	if IsReadOnly("bash", `{"script":"terraform apply"}`, cmds) {
		t.Error("'terraform apply' must not be read-only")
	}
}
