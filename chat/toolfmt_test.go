package chat

import "testing"

func TestFormatToolCall_Fallback(t *testing.T) {
	// Unknown tool → humanized, sorted key: value lines.
	got := FormatToolCall("some_mcp_tool", `{"beta":"two","alpha":1}`)
	want := "alpha: 1\nbeta: two"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatToolCall_FallbackMultiline(t *testing.T) {
	// Multi-line / long string values go on their own indented block.
	got := FormatToolCall("x", `{"body":"line one\nline two"}`)
	want := "body:\n  line one\n  line two"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatToolCall_InvalidJSON(t *testing.T) {
	in := "not json at all"
	if got := FormatToolCall("x", in); got != in {
		t.Fatalf("got %q want %q", got, in)
	}
}

func TestFormatToolCall_KnownTools(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args string
		want string
	}{
		{"bash", "bash", `{"script":"ls -la"}`, "$ ls -la"},
		{"bash timeout", "bash", `{"script":"sleep 1","timeout":60}`, "$ sleep 1  (timeout 60s)"},
		{"bash_background", "bash_background", `{"script":"make build"}`, "$ make build  (background)"},
		{"bash_jobs", "bash_jobs", `{}`, "list shell jobs"},
		{"bash_job_output", "bash_job_output", `{"job_id":"bg-2"}`, "job output bg-2"},
		{"bash_job_kill", "bash_job_kill", `{"job_id":"bg-2"}`, "kill job bg-2"},
		{"read", "read", `{"path":"main.go"}`, "read main.go"},
		{"read range", "read", `{"path":"main.go","offset":10,"limit":20}`, "read main.go  (lines 10–30)"},
		{"write", "write", `{"path":"out.txt","content":"hi"}`, "write out.txt"},
		{"edit", "edit", `{"path":"a.go","old":"foo","new":"bar"}`, "edit a.go\n  foo → bar"},
		{"glob", "glob", `{"pat":"**/*.go"}`, "glob **/*.go in ."},
		{"glob path", "glob", `{"pat":"*.md","path":"docs"}`, "glob *.md in docs"},
		{"grep", "grep", `{"pat":"TODO"}`, "grep /TODO/ in ."},
		{"load_skill", "load_skill", `{"name":"frontend-design"}`, "load skill frontend-design"},
		{"ask_user", "ask_user", `{"question":"Proceed?"}`, "ask: Proceed?"},
		{"agent_logs", "agent_logs", `{"agent_id":"a1b2"}`, "agent logs a1b2"},
		{"schedule_wakeup", "schedule_wakeup", `{"delay_seconds":600,"note":"check build"}`, "wake in 600s — check build"},
		{"cron", "cron", `{"expr":"*/5 * * * *","prompt":"/foo"}`, "cron */5 * * * * → /foo"},
		{"cron once", "cron", `{"expr":"0 9 * * *","prompt":"/foo","recurring":false}`, "cron 0 9 * * * → /foo (once)"},
		{"cron durable", "cron", `{"expr":"0 9 * * *","prompt":"/foo","durable":true}`, "cron 0 9 * * * → /foo (durable)"},
		{"cron_list", "cron_list", `{}`, "list cron jobs"},
		{"cron_delete", "cron_delete", `{"id":"loop-1"}`, "cancel cron loop-1"},
		{"spawn_agent", "spawn_agent", `{"agent_type":"researcher","task":"find docs"}`, "spawn researcher: find docs"},
		{"check_agent", "check_agent", `{"agent_id":"a1b2"}`, "check agent a1b2"},
		{"get_agent_result", "get_agent_result", `{"agent_id":"a1b2"}`, "result of agent a1b2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatToolCall(tc.tool, tc.args); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
