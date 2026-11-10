package chat

import (
	"context"
	"encoding/json"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"strings"
	"testing"
	"time"
)

func TestHostWaitForPassesTheSourceAndLeavesTimesToTheHost(t *testing.T) {
	var got WaitRequest
	tool := &hostWaitForTool{arm: func(r WaitRequest) string { got = r; return "armed" }}
	out, _, err := tool.Run(map[string]any{
		"tool": "list_downloads", "arguments": map[string]any{"state": "active"},
		"until": "json_equals", "field": "items.0.status", "equals": "done",
		"then": "tell me", "reason": "the download",
	})
	if err != nil || out != "armed" {
		t.Fatalf("out %q err %v", out, err)
	}
	if got.Tool != "list_downloads" || got.Arguments["state"] != "active" || got.Field != "items.0.status" || got.Equals != "done" || got.Reason != "the download" {
		t.Errorf("request lost a field: %+v", got)
	}
	if got.Timeout != 0 || got.IntervalSeconds != 0 {
		t.Errorf("the times were defaulted by nib: %v %d", got.Timeout, got.IntervalSeconds)
	}
}

func TestHostWaitForRefusesBadSources(t *testing.T) {
	tool := &hostWaitForTool{arm: func(WaitRequest) string { t.Fatal("armed a bad wait"); return "" }}
	for name, args := range map[string]map[string]any{
		"none":    {"then": "x"},
		"both":    {"tool": "a", "url": "http://x", "then": "x"},
		"script":  {"script": "ls", "then": "x"},
		"no then": {"url": "http://x"},
	} {
		if out, _, _ := tool.Run(args); out == "" {
			t.Errorf("%s: no refusal", name)
		}
	}
}

func TestHostWaitSchemasAreObjectsWithTheHostFields(t *testing.T) {
	def := hostWaitForToolDefinition(func(WaitRequest) string { return "" }, false, false).Tool()
	b, _ := json.Marshal(def.Function.Parameters)
	for _, want := range []string{`"tool"`, `"json_equals"`, `"field"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("schema lacks %s: %s", want, b)
		}
	}
	if strings.Contains(string(b), `"script"`) || strings.Contains(string(b), `exit_zero`) {
		t.Errorf("the host schema offers a script: %s", b)
	}
	if hostWaitAgentsToolDefinition(context.Background(), nil).Tool().Function.Name != "wait_agents" {
		t.Error("wrong name")
	}
}

func TestHostWaitAgentsPassesTheRequestAndTheTurnContext(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, 1)
	var gotCtx context.Context
	var got WaitAgentsRequest
	tool := &hostWaitAgentsTool{ctx: ctx, run: func(c context.Context, r WaitAgentsRequest) string { gotCtx, got = c, r; return "done" }}
	out, _, _ := tool.Run(map[string]any{"ids": []any{"a", "b"}, "mode": "any_failed_or_all", "timeout_seconds": float64(20)})
	if out != "done" || gotCtx.Value(key{}) != 1 || len(got.IDs) != 2 || got.Mode != "any_failed_or_all" || got.Timeout != 20*time.Second {
		t.Errorf("out %q ctx %v req %+v", out, gotCtx, got)
	}
}

func TestWaitToolsReadJSONNumbers(t *testing.T) {
	// The tool loop decodes numbers as json.Number.
	var got WaitRequest
	tool := &hostWaitForTool{arm: func(r WaitRequest) string { got = r; return "ok" }}
	tool.Run(map[string]any{"url": "http://x", "then": "t", "timeout_seconds": json.Number("90"), "interval_seconds": json.Number("30"), "max_errors": json.Number("2")})
	if got.Timeout != 90*time.Second || got.IntervalSeconds != 30 || got.MaxErrors != 2 {
		t.Errorf("numbers lost: %+v", got)
	}
	var gotA WaitAgentsRequest
	wa := &hostWaitAgentsTool{ctx: context.Background(), run: func(_ context.Context, r WaitAgentsRequest) string { gotA = r; return "" }}
	wa.Run(map[string]any{"timeout_seconds": json.Number("20")})
	if gotA.Timeout != 20*time.Second {
		t.Errorf("timeout lost: %v", gotA.Timeout)
	}
}

func TestHostTransportToolsPassTheAllowlistAndReplaceBuiltins(t *testing.T) {
	s := newReloadTestSession(t)
	host := connectTestMCP(t, "host", 1, 10)
	other := connectTestMCP(t, "other", 1, 10)
	s.hostClients = map[*sdkmcp.ClientSession]bool{host: true}
	s.hostNames = map[string]bool{"cron": true}
	s.toolAllow = map[string]bool{"memory": true}

	// The filter is rebuilt per call, as its comment requires.
	f := s.mcpToolFilter()
	if !f(host, "host_0") {
		t.Error("a host tool not in the allowlist must pass")
	}
	if f(other, "other_0") {
		t.Error("a tool of another transport is still gated by the allowlist")
	}
	if !f(other, "memory") {
		t.Error("the allowlist still admits what it names")
	}
	if s.toolEnabled("cron") {
		t.Error("a built-in is registered although the host serves a tool of that name")
	}
	s.toolAllow = nil
	if s.toolEnabled("cron") {
		t.Error("an empty allowlist must not bring the replaced built-in back")
	}
	if !s.toolEnabled("tree") {
		t.Error("an empty allowlist still enables the other built-ins")
	}
}

func TestHostWaitForWithoutScriptsKeepsTheRefusal(t *testing.T) {
	tool := &hostWaitForTool{arm: func(WaitRequest) string { t.Fatal("armed"); return "" }}
	out, _, _ := tool.Run(map[string]any{"script": "ls", "then": "x"})
	if out != "wait_for cannot run a script in this session: use a tool or a url." {
		t.Errorf("refusal changed: %q", out)
	}
	if out, _, _ := tool.Run(map[string]any{"url": "http://x", "until": "exit_zero", "then": "x"}); out == "" {
		t.Error("exit_zero accepted without scripts")
	}
}

func TestHostWaitForScriptsPassesTheScriptAndExitZero(t *testing.T) {
	var got WaitRequest
	tool := &hostWaitForTool{scripts: true, arm: func(r WaitRequest) string { got = r; return "armed" }}
	out, _, _ := tool.Run(map[string]any{"script": " test -f /x ", "until": "exit_zero", "then": "go", "interval_seconds": float64(30)})
	if out != "armed" || got.Script != "test -f /x" || got.Until != "exit_zero" || got.IntervalSeconds != 30 || got.Tool != "" || got.URL != "" {
		t.Errorf("out %q req %+v", out, got)
	}
	// The other conditions work on a script too.
	got = WaitRequest{}
	tool.Run(map[string]any{"script": "echo", "until": "match", "pattern": "a", "then": "go"})
	if got.Script != "echo" || got.Until != "match" || got.Pattern != "a" {
		t.Errorf("req %+v", got)
	}
}

func TestHostWaitForScriptsValidatesTheSource(t *testing.T) {
	tool := &hostWaitForTool{scripts: true, arm: func(WaitRequest) string { t.Fatal("armed a bad wait"); return "" }}
	for name, args := range map[string]map[string]any{
		"none":                {"then": "x"},
		"script and url":      {"script": "ls", "url": "http://x", "then": "x"},
		"script and tool":     {"script": "ls", "tool": "t", "then": "x"},
		"all three":           {"script": "ls", "tool": "t", "url": "http://x", "then": "x"},
		"exit_zero on a url":  {"url": "http://x", "until": "exit_zero", "then": "x"},
		"exit_zero on a tool": {"tool": "t", "until": "exit_zero", "then": "x"},
		"unknown until":       {"script": "ls", "until": "never", "then": "x"},
		"no then":             {"script": "ls"},
	} {
		out, _, _ := tool.Run(args)
		if out == "" {
			t.Errorf("%s: no refusal", name)
		}
	}
	out, _, _ := tool.Run(map[string]any{"script": "ls", "url": "u", "then": "x"})
	if !strings.Contains(out, "one of tool, url or script") {
		t.Errorf("unclear error: %q", out)
	}
}

func TestHostWaitForScriptSchema(t *testing.T) {
	def := hostWaitForToolDefinition(func(WaitRequest) string { return "" }, true, false).Tool()
	b, _ := json.Marshal(def.Function.Parameters)
	for _, want := range []string{`"script"`, `"exit_zero"`, `"json_equals"`, `"tool"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("schema lacks %s: %s", want, b)
		}
	}
	if def.Function.Name != "wait_for" {
		t.Errorf("name %q", def.Function.Name)
	}
}

func jobTool(scripts bool) (*hostWaitForTool, *WaitRequest) {
	var got WaitRequest
	return &hostWaitForTool{arm: func(r WaitRequest) string { got = r; return "armed" }, scripts: scripts, jobs: true}, &got
}

func TestHostWaitJobsSchemaOnAndOff(t *testing.T) {
	arm := func(WaitRequest) string { return "" }
	on, _ := json.Marshal(hostWaitForToolDefinition(arm, false, true).Tool().Function.Parameters)
	for _, want := range []string{`"job"`, `"idle"`, `"exit"`, `"stall"`, `"json_equals"`} {
		if !strings.Contains(string(on), want) {
			t.Errorf("schema lacks %s: %s", want, on)
		}
	}
	if strings.Contains(string(on), `"script"`) || strings.Contains(string(on), `exit_zero`) {
		t.Errorf("jobs without scripts offers a script: %s", on)
	}
	both, _ := json.Marshal(hostWaitForToolDefinition(arm, true, true).Tool().Function.Parameters)
	if !strings.Contains(string(both), `"exit_zero"`) || !strings.Contains(string(both), `"stall"`) {
		t.Errorf("scripts and jobs: %s", both)
	}
	desc := hostWaitForToolDefinition(arm, false, true).Tool().Function.Description
	if !strings.Contains(desc, "background job") || !strings.Contains(desc, "until=stall") {
		t.Errorf("description: %s", desc)
	}
	for _, scripts := range []bool{false, true} {
		off, _ := json.Marshal(hostWaitForToolDefinition(arm, scripts, false).Tool().Function.Parameters)
		for _, bad := range []string{`"job"`, `"idle"`, `"stall"`, `"exit"`} {
			if strings.Contains(string(off), bad) {
				t.Errorf("flag off (scripts %v) leaks %s", scripts, bad)
			}
		}
		d := hostWaitForToolDefinition(arm, scripts, false).Tool().Function.Description
		if strings.Contains(d, "job you started") {
			t.Errorf("flag off description mentions jobs: %s", d)
		}
	}
	// Schema and description are byte-identical to the old constants.
	if d := hostWaitForToolDefinition(arm, false, false).Tool().Function.Description; d != hostWaitForDescription {
		t.Error("flag-off description changed")
	}
	if d := hostWaitForToolDefinition(arm, true, false).Tool().Function.Description; d != hostWaitForScriptDescription {
		t.Error("flag-off script description changed")
	}
	for _, p := range []map[string]any{hostWaitForSchema, hostWaitForScriptSchema} {
		props := p["properties"].(map[string]any)
		if _, ok := props["job"]; ok {
			t.Error("shared schema has job")
		}
		if _, ok := props["idle"]; ok {
			t.Error("shared schema has idle")
		}
	}
}

func TestHostWaitJobsFlagOffIgnoresJob(t *testing.T) {
	tool := &hostWaitForTool{arm: func(WaitRequest) string { t.Fatal("armed"); return "" }}
	out, _, _ := tool.Run(map[string]any{"job": "j1", "until": "exit", "then": "x"})
	if !strings.Contains(out, "Unknown until") && !strings.Contains(out, "needs a tool or a url") {
		t.Errorf("out %q", out)
	}
	out, _, _ = tool.Run(map[string]any{"job": "j1", "then": "x"})
	if out != "wait_for needs a tool or a url to check." {
		t.Errorf("refusal changed: %q", out)
	}
}

func TestHostWaitJobsPassesJobIdleUntil(t *testing.T) {
	tool, got := jobTool(false)
	out, _, _ := tool.Run(map[string]any{"job": "j1", "until": "stall", "idle": json.Number("120"), "then": "t"})
	if out != "armed" || got.Job != "j1" || got.Idle != 2*time.Minute || got.Until != "stall" || got.Script != "" {
		t.Errorf("out %q req %+v", out, *got)
	}
	tool, got = jobTool(false)
	tool.Run(map[string]any{"job": "j1", "until": "stall", "then": "t"})
	if got.Idle != 10*time.Minute {
		t.Errorf("default idle %v", got.Idle)
	}
	for _, until := range []string{"exit", "match", "change", "json_equals", ""} {
		tool, got = jobTool(false)
		tool.Run(map[string]any{"job": "j1", "until": until, "pattern": "p", "then": "t"})
		if got.Job != "j1" || got.Until != until || got.Idle != 0 {
			t.Errorf("until %q: %+v", until, *got)
		}
	}
}

func TestHostWaitJobsValidation(t *testing.T) {
	for name, tc := range map[string]struct {
		scripts bool
		args    map[string]any
		want    string
	}{
		"none":               {false, map[string]any{"then": "x"}, "tool, url or job"},
		"none scripts":       {true, map[string]any{"then": "x"}, "tool, url, script or job"},
		"job and url":        {false, map[string]any{"job": "j", "url": "http://x", "then": "x"}, "not several"},
		"job and script":     {true, map[string]any{"job": "j", "script": "ls", "then": "x"}, "not several"},
		"script not run":     {false, map[string]any{"script": "ls", "then": "x"}, "cannot run a script"},
		"exit no job":        {false, map[string]any{"url": "http://x", "until": "exit", "then": "x"}, "until=exit needs a job"},
		"stall no job":       {true, map[string]any{"script": "ls", "until": "stall", "then": "x"}, "until=stall needs a job"},
		"exit_zero on job":   {true, map[string]any{"job": "j", "until": "exit_zero", "then": "x"}, "exit_zero needs a script"},
		"idle without stall": {false, map[string]any{"job": "j", "until": "exit", "idle": 120, "then": "x"}, "only for until=stall"},
		"idle default until": {false, map[string]any{"job": "j", "idle": 120, "then": "x"}, "only for until=stall"},
		"idle too small":     {false, map[string]any{"job": "j", "until": "stall", "idle": 59, "then": "x"}, "between 60 and 3600"},
		"idle too big":       {false, map[string]any{"job": "j", "until": "stall", "idle": 3601, "then": "x"}, "between 60 and 3600"},
		"idle negative":      {false, map[string]any{"job": "j", "until": "stall", "idle": -5, "then": "x"}, "between 60 and 3600"},
		"unknown until":      {false, map[string]any{"job": "j", "until": "boom", "then": "x"}, "Unknown until"},
		"no then":            {false, map[string]any{"job": "j"}, "needs then"},
	} {
		tool := &hostWaitForTool{arm: func(WaitRequest) string { t.Fatalf("%s: armed", name); return "" }, scripts: tc.scripts, jobs: true}
		if out, _, _ := tool.Run(tc.args); !strings.Contains(out, tc.want) {
			t.Errorf("%s: %q lacks %q", name, out, tc.want)
		}
	}
	for _, idle := range []int{60, 3600} {
		tool, got := jobTool(false)
		if out, _, _ := tool.Run(map[string]any{"job": "j", "until": "stall", "idle": idle, "then": "x"}); out != "armed" || got.Idle != time.Duration(idle)*time.Second {
			t.Errorf("idle %d: %q %+v", idle, out, *got)
		}
	}
	// Scripts and tools still work next to jobs, exit_zero too.
	tool, got := jobTool(true)
	tool.Run(map[string]any{"script": "ls", "until": "exit_zero", "then": "x"})
	if got.Script != "ls" || got.Until != "exit_zero" {
		t.Errorf("%+v", *got)
	}
}
