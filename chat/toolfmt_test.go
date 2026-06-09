package chat

import (
	"strings"
	"testing"
)

func TestFormatToolCall_Fallback(t *testing.T) {
	// Keys sorted and padded to a column; no JSON syntax.
	got := FormatToolCall("some_mcp_tool", `{"beta":"two","alpha":1}`)
	want := "alpha  1\nbeta   two"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFormatToolCall_FallbackMultiline(t *testing.T) {
	// Multi-line values show their first line plus a hidden-line count.
	got := FormatToolCall("x", `{"body":"line one\nline two"}`)
	want := "body  line one… (+1 line)"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFormatToolCall_FallbackNested(t *testing.T) {
	// Nested objects flatten to dotted keys; scalar arrays join with ", ";
	// object arrays flatten with an index. No braces, brackets, or quotes.
	got := FormatToolCall("x", `{"server":{"url":"http://x"},"labels":["bug","auth"],"items":[{"name":"a"}],"count":2}`)
	want := "count         2\nitems.0.name  a\nlabels        bug, auth\nserver.url    http://x"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	for _, forbidden := range []string{"{", "}", "[", "]", `"`} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("fallback output contains JSON syntax %q: %q", forbidden, got)
		}
	}
}

func TestToolArgRows(t *testing.T) {
	// Unknown tool with object args → rows.
	rows, ok := ToolArgRows("some_mcp_tool", `{"body":"l1\nl2\nl3","title":"x"}`)
	if !ok || len(rows) != 2 {
		t.Fatalf("expected 2 rows ok=true, got %v ok=%v", rows, ok)
	}
	if rows[0].Key != "body" || rows[0].Value != "l1" || rows[0].HiddenLines != 2 {
		t.Fatalf("unexpected body row: %+v", rows[0])
	}
	if rows[0].ValueDisplay() != "l1… (+2 lines)" {
		t.Fatalf("unexpected ValueDisplay: %q", rows[0].ValueDisplay())
	}
	if rows[1].Key != "title" || rows[1].Value != "x" || rows[1].HiddenLines != 0 {
		t.Fatalf("unexpected title row: %+v", rows[1])
	}

	// Known tools keep their purpose-built one-liners — no rows.
	if _, ok := ToolArgRows("bash", `{"script":"ls"}`); ok {
		t.Fatal("known formatter tools must not return rows")
	}
	// Non-object args → no rows.
	if _, ok := ToolArgRows("x", `not json`); ok {
		t.Fatal("invalid JSON must not return rows")
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
		{"schedule_wakeup note", "schedule_wakeup", `{"delay_seconds":600,"note":"check build"}`, "wake in 600s — check build"},
		{"schedule_wakeup prompt", "schedule_wakeup", `{"delay_seconds":600,"prompt":"resume work"}`, "wake in 600s — resume work"},
		{"schedule_wakeup reason", "schedule_wakeup", `{"delay_seconds":600,"prompt":"resume work","reason":"build pending"}`, "wake in 600s — build pending"},
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
