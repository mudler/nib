package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/cogito"
	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
)

// A completed background agent resumes through send_agent_message, not the
// normal spawn path. Its tool approval must retain child attribution even
// though Cogito does not propagate tool-result callbacks into sub-agents.
func TestResumedAgentPreservesToolLifecycleAttribution(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var mu sync.Mutex
	var observations []Observation
	var starts []ToolStart
	var results []ToolResult
	var calls atomic.Int64
	var rootCalls, childCalls atomic.Int64
	parentContinued := make(chan struct{})
	childContinued := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		isChild := false
		for _, message := range req.Messages {
			if message.Role == "user" && message.Content == "synthetic child task" {
				isChild = true
			}
		}
		var localCall int64
		if isChild {
			// Force the parent continuation to arrive first: response routing
			// must not depend on scheduling of the resumed background child.
			select {
			case <-parentContinued:
			case <-ctx.Done():
				return
			}
			localCall = childCalls.Add(1)
		} else {
			localCall = rootCalls.Add(1)
		}
		calls.Add(1)
		if !isChild && localCall == 2 {
			close(parentContinued)
		}
		msg := map[string]any{"role": "assistant", "content": "done"}
		tool := func(name, args string) {
			msg["content"] = ""
			msg["tool_calls"] = []any{map[string]any{"id": "synthetic-call", "type": "function", "function": map[string]any{"name": name, "arguments": args}}}
		}
		switch {
		case !isChild && localCall == 1:
			tool("send_agent_message", `{"agent_id":"finished-child","message":"continue"}`)
		case isChild && localCall == 1:
			// Only the resumed child's retained context receives the probe.
			tool("probe", `{}`)
		case isChild:
			msg["content"] = "child done"
			if localCall == 2 {
				close(childContinued)
			}
		default:
			// The child's next model request proves its probe result callbacks
			// have run before the parent finishes and assertions inspect them.
			select {
			case <-childContinued:
			case <-ctx.Done():
				return
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"id": "synthetic", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "stop"}}})
	}))
	defer srv.Close()
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "synthetic", Version: "v0"}, nil)
	executed := make(chan struct{}, 1)
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "probe", Description: "synthetic fast probe"}, func(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, any, error) {
		executed <- struct{}{}
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "probe done"}}}, nil, nil
	})
	mcpServer := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil))
	defer mcpServer.Close()
	session, err := NewSession(ctx, types.Config{Model: "synthetic", APIKey: "synthetic", BaseURL: srv.URL + "/v1", ApprovalMode: "auto", WorkingDir: t.TempDir(), AgentOptions: types.AgentOptions{Iterations: 5, MaxAttempts: 1, MaxRetries: 1}}, Callbacks{
		ObservationCallbacks: func() func(Observation) {
			return func(o Observation) { mu.Lock(); defer mu.Unlock(); observations = append(observations, o) }
		},
		OnToolStart:  func(s ToolStart) { mu.Lock(); defer mu.Unlock(); starts = append(starts, s) },
		OnToolResult: func(r ToolResult) { mu.Lock(); defer mu.Unlock(); results = append(results, r) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.ReconcileMCPServers(map[string]types.MCPServer{"synthetic": {URL: mcpServer.URL}}); err != nil {
		t.Fatal(err)
	}
	frag := cogito.NewEmptyFragment().AddMessage(cogito.UserMessageRole, "synthetic child task").AddMessage(cogito.AssistantMessageRole, "previous result")
	session.agentManager.Register(&cogito.AgentState{ID: "finished-child", Status: cogito.AgentStatusCompleted, Background: true, Fragment: &frag, Result: "previous result"})
	if _, err := session.SendMessage("resume the finished child"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-executed:
	default:
		t.Fatal("child probe did not execute")
	}
	mu.Lock()
	defer mu.Unlock()
	parentResult := false
	childTagged := false
	for _, r := range results {
		if r.Name == "send_agent_message" {
			parentResult = true
			if !strings.Contains(r.Result, "resumed in the background") {
				t.Errorf("send_agent_message result = %q, want asynchronous acknowledgement", r.Result)
			}
			t.Logf("resume result: %s", r.Result)
		}
		if r.Name == "probe" && r.AgentID == "finished-child" {
			childTagged = true
		}
	}
	if !parentResult {
		t.Fatal("resume did not deliver parent result")
	}
	for _, s := range starts {
		if s.Name == "probe" {
			matched := false
			for _, r := range results {
				if r.ID == s.ID {
					matched = true
				}
			}
			t.Errorf("resumed child emitted ROOT start; matching result=%v", matched)
		}
	}
	if !childTagged {
		t.Error("resumed child tool missing agent-tagged thread event")
	}
	t.Logf("requests=%d root starts=%d result events=%d", calls.Load(), len(starts), len(results))
	childRequest, childStart := false, false
	for _, o := range observations {
		if o.OwnerKnown && o.Owner == "finished-child" {
			if o.HasText {
				t.Errorf("fabricated resumed child text: %+v", o)
			}
			childRequest = childRequest || o.Kind == "tool requested"
			childStart = childStart || o.Kind == "tool started"
		}
	}
	if !childRequest || !childStart {
		t.Fatalf("missing resumed child receipts: %+v", observations)
	}

}
