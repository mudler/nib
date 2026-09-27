package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/nib/types"
)

func TestBashScript(t *testing.T) {
	for _, tc := range []struct {
		args   string
		want   string
		wantOK bool
	}{
		{`{"script":"ls -la"}`, "ls -la", true},
		{`{"command":"ls -la"}`, "ls -la", true},
		{`{"script":"ls","command":"ls"}`, "ls", true},
		// Two different scripts: nothing may guess which one runs.
		{`{"script":"ls","command":"rm -rf /"}`, "", false},
		{`{}`, "", false},
		{`{"script":"  "}`, "", false},
		{`not json`, "", false},
	} {
		got, ok := BashScript(tc.args)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("BashScript(%s) = %q, %v; want %q, %v", tc.args, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestCanonicalBashArgs(t *testing.T) {
	for _, tc := range []struct {
		tool, args, want string
	}{
		{"bash", `{"command":"ls","timeout":5}`, `{"script":"ls","timeout":5}`},
		{"bash_background", `{"command":"make"}`, `{"script":"make"}`},
		{"bash", `{"script":"ls","command":"ls"}`, `{"script":"ls"}`},
		// Shell operators stay as written, not \u0026 / \u003e, so a hook that
		// matches the raw text sees the same thing for either key.
		{"bash", `{"command":"make && cat a > b < c"}`, `{"script":"make && cat a > b < c"}`},
		{"bash", `{"command":"x","note":"<&>"}`, `{"note":"<&>","script":"x"}`},
		// Unchanged: already canonical, conflicting, empty, other tools, bad JSON.
		{"bash", `{"script":"ls"}`, `{"script":"ls"}`},
		{"bash", `{"script":"ls","command":"pwd"}`, `{"script":"ls","command":"pwd"}`},
		{"bash", `{"command":""}`, `{"command":""}`},
		{"read", `{"command":"ls"}`, `{"command":"ls"}`},
		{"bash", `nope`, `nope`},
	} {
		if got := CanonicalBashArgs(tc.tool, tc.args); got != tc.want {
			t.Errorf("CanonicalBashArgs(%s, %s) = %s, want %s", tc.tool, tc.args, got, tc.want)
		}
	}
}

// startShellClient serves the shell tools over an in-memory transport and
// returns a connected client session, so a call goes through the same input
// schema validation a model's call does.
func startShellClient(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	limits := NewOutputLimitsPolicy(types.ToolOutputLimitsConfig{})
	go func() { _ = startBashMCPServer(ctx, serverTransport, newBgJobManager(), limits, NewArtifactStore()) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1.0.0"}, nil)
	sess, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

func callText(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s(%v): %v", name, args, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func TestBashAcceptsCommandAlias(t *testing.T) {
	sess := startShellClient(t)
	text, isErr := callText(t, sess, "bash", map[string]any{"command": "echo hi"})
	if isErr {
		t.Fatalf("bash with command errored: %s", text)
	}
	var out executeCommandOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode %q: %v", text, err)
	}
	if strings.TrimSpace(out.Stdout) != "hi" || out.Script != "echo hi" {
		t.Fatalf("output = %+v, want stdout hi and script echo hi", out)
	}
}

func TestBashBackgroundAcceptsCommandAlias(t *testing.T) {
	sess := startShellClient(t)
	text, isErr := callText(t, sess, "bash_background", map[string]any{"command": "echo hi"})
	if isErr {
		t.Fatalf("bash_background with command errored: %s", text)
	}
	var started bgStartOutput
	if err := json.Unmarshal([]byte(text), &started); err != nil {
		t.Fatalf("decode %q: %v", text, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		text, _ = callText(t, sess, "bash_job_output", map[string]any{"job_id": started.JobID})
		if strings.Contains(text, "hi") && !strings.Contains(text, `"running"`) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s output = %s, want hi", started.JobID, text)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestBashScriptArgumentErrors(t *testing.T) {
	sess := startShellClient(t)
	for _, tool := range []string{"bash", "bash_background"} {
		text, isErr := callText(t, sess, tool, map[string]any{})
		if !isErr || !strings.Contains(text, `missing required argument "script"`) {
			t.Errorf("%s with no script: isError %v, text %q; want the missing-script error", tool, isErr, text)
		}
		text, isErr = callText(t, sess, tool, map[string]any{"script": "ls", "command": "pwd"})
		if !isErr || !strings.Contains(text, `"script"`) {
			t.Errorf("%s with two scripts: isError %v, text %q; want an error naming script", tool, isErr, text)
		}
	}
}
