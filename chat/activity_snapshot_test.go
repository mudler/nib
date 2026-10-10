package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
func TestActivitySnapshotLifecycle(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var mu sync.Mutex
	var observations []Observation
	var starts []ToolStart
	var results []ToolResult
	var calls atomic.Int64
	var rootCalls, childCalls atomic.Int64
	var session *Session
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
			if localCall == 1 {
				// This is the real resumed request, before its completion callback.
				snapshot := session.ActivitySnapshot()
				if len(snapshot.Agents) != 1 || !snapshot.Agents[0].Running || snapshot.Agents[0].ID != "finished-child" {
					t.Errorf("resumed snapshot missing child: %+v", snapshot)
				}
				if got := session.background.terminalSnapshot().runningAgents; got != 1 {
					t.Errorf("resumed child executing but authoritative running agents = %d, want 1", got)
				}
			}
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

func TestActivitySnapshotCompletionStages(t *testing.T) {
	stages := []struct {
		name   string
		setup  func(*backgroundState)
		reason func(CompletionSnapshot) bool
	}{
		{"publisher", func(b *backgroundState) { b.beginPublisher("p") }, func(c CompletionSnapshot) bool { return c.Publishers == 1 }},
		{"queued", func(b *backgroundState) {
			b.startBackground(backgroundAgent, "a")
			b.completeBackground(backgroundAgent, "a", "done", true)
		}, func(c CompletionSnapshot) bool { return c.QueuedNotices == 1 }},
		{"reserved", func(b *backgroundState) {
			b.startBackground(backgroundAgent, "a")
			b.completeBackground(backgroundAgent, "a", "done", true)
			b.reserveNotices(0)
		}, func(c CompletionSnapshot) bool { return c.ReservedNotices == 1 }},
		{"observation", func(b *backgroundState) {
			b.startBackground(backgroundAgent, "a")
			b.completeBackground(backgroundAgent, "a", "done", true)
			r, _ := b.reserveNotices(0)
			b.consumeNotices(r)
		}, func(c CompletionSnapshot) bool { return c.EventSequence > c.RootObservedSequence }},
		{"supervisor queued", func(b *backgroundState) { b.mu.Lock(); b.reviewQueued = true; b.mu.Unlock() }, func(c CompletionSnapshot) bool { return c.SupervisorQueued }},
		{"supervisor reviewing", func(b *backgroundState) { b.setReviewing(true) }, func(c CompletionSnapshot) bool { return c.SupervisorReviewing }},
	}
	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			b := newBackgroundState()
			stage.setup(b)
			got := (&Session{background: b}).ActivitySnapshot()
			if !got.Known || !got.Coherent || !got.Barrier.Known || !stage.reason(got.Barrier) || got.ReadyAllowed {
				t.Fatalf("missing completion reason or false readiness: %+v", got)
			}
		})
	}
}

func TestActivitySnapshotConcurrentPublication(t *testing.T) {
	b := newBackgroundState()
	s := &Session{background: b}
	b.startBackground(backgroundAgent, "a")
	first := s.ActivitySnapshot()
	if len(first.Agents) != 1 {
		t.Fatalf("running identity missing: %+v", first)
	}
	first.Agents[0].ID = "mutated"
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var rev uint64
			for j := 0; j < 100; j++ {
				got := s.ActivitySnapshot()
				if got.Revision < rev {
					t.Error("revision regressed")
				}
				rev = got.Revision
				if len(got.Agents) > 0 && got.Agents[0].ID != "a" {
					t.Error("snapshot aliases owner")
				}
			}
		}()
	}
	for i := 0; i < 100; i++ {
		b.setRoot(true, true, false)
		b.interrupt()
		b.setRoot(false, false, false)
	}
	wg.Wait()
	b.close()
	b.reopenBackground(backgroundAgent, "stale")
	if got := s.ActivitySnapshot(); got.Known || got.ReadyAllowed {
		t.Fatalf("closed generation remains available: %+v", got)
	}
	fresh := (&Session{background: newBackgroundState()}).ActivitySnapshot()
	if fresh.Generation == first.Generation {
		t.Fatal("new session reused generation")
	}
}

func TestActivitySnapshotSettledOutcomes(t *testing.T) {
	stubRetrySleep(t)
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			var s *Session
			if cancelled {
				llm := &interruptingRateLimitLLM{rateLimitLLM: rateLimitLLM{failures: 99}}
				s = newRateLimitSession(t, llm)
				llm.s = s
			} else {
				s = newRateLimitSession(t, &rateLimitLLM{failures: 99, err: errors.New("localai stream: status 401: invalid api key")})
			}
			s.background = newBackgroundState()
			if _, err := s.SendMessage("test outcome"); err == nil {
				t.Fatal("outcome lost")
			}
			got := s.ActivitySnapshot()
			if !got.ReadyAllowed || got.RootActive || got.RootParked {
				t.Fatalf("settled turn unavailable: %+v", got)
			}
			if cancelled && s.background.terminalSnapshot().eligible {
				t.Fatal("interruption must not count as successful completion")
			}
		})
	}
}

func TestActivitySnapshotOwnerTransitions(t *testing.T) {
	s := &Session{background: newBackgroundState()}
	if !(s.ActivitySnapshot().ReadyAllowed) {
		t.Fatal("new initialized session unavailable")
	}
	// Synchronous re-entry proves callbacks run outside the ledger lock.
	callback := func(e AgentEvent) {
		got := s.ActivitySnapshot()
		if len(got.Agents) != 1 || got.Agents[0].Running != (e.Status == AgentStatusRunning) {
			t.Errorf("notification before publication: %+v", got)
		}
	}
	s.background.setRoot(true, true, false)
	s.emitAgentEventTo(&cogito.AgentState{ID: "child", Status: cogito.AgentStatusRunning}, callback)
	got := s.ActivitySnapshot()
	if !got.RootActive || got.ReadyAllowed || got.Agents[0].Background || len(got.Shells) != 0 {
		t.Fatalf("foreground execution: %+v", got)
	}
	s.background.setRoot(true, false, true)
	got = s.ActivitySnapshot()
	if got.RootActive || !got.RootParked {
		t.Fatalf("parked: %+v", got)
	}
	s.background.setRoot(true, true, false)
	if !s.ActivitySnapshot().RootActive {
		t.Fatal("resumed review not active")
	}
	s.endTurn()
	if s.ActivitySnapshot().ReadyAllowed {
		t.Fatal("post-turn child lost")
	}
	s.emitAgentEventTo(&cogito.AgentState{ID: "child", Status: cogito.AgentStatusFailed}, callback)
	if !s.ActivitySnapshot().ReadyAllowed {
		t.Fatal("foreground failure remains busy")
	}
	s.background.startBackground(backgroundShell, "sh")
	s.background.completeBackground(backgroundShell, "sh", "failed", false)
	got = s.ActivitySnapshot()
	if got.Shells[0].Running || !got.Shells[0].Background || got.ReadyAllowed {
		t.Fatalf("shell completion bypassed review: %+v", got)
	}
	r, _, observed := s.background.reserveNoticesForRoot(0)
	s.background.consumeNotices(r)
	s.background.markRootObserved(observed)
	if !s.ActivitySnapshot().ReadyAllowed {
		t.Fatal("reviewed shell blocks readiness")
	}
	old := s.background
	old.close()
	fresh := &Session{background: newBackgroundState()}
	before := fresh.ActivitySnapshot()
	old.childActivity("late", true, true)
	old.reopenBackground(backgroundAgent, "late")
	after := fresh.ActivitySnapshot()
	if after.Generation == got.Generation || before.Revision != after.Revision || !after.ReadyAllowed {
		t.Fatalf("stale callback affected replacement: %+v", after)
	}
}

func TestActivitySnapshotRevisionAndUnknown(t *testing.T) {
	for _, s := range []*Session{nil, {}} {
		if got := s.ActivitySnapshot(); got.Known || got.ReadyAllowed {
			t.Fatalf("uninitialized known: %+v", got)
		}
	}
	b := newBackgroundState()
	s := &Session{background: b}
	prev := s.ActivitySnapshot().Revision
	check := func() {
		t.Helper()
		now := s.ActivitySnapshot().Revision
		if now <= prev {
			t.Fatalf("mutation not revisioned: %d <= %d", now, prev)
		}
		prev = now
	}
	b.beginPublisher("p")
	check()
	b.endPublisher("p")
	check()
	b.startBackground(backgroundAgent, "a")
	check()
	b.completeBackground(backgroundAgent, "a", "done", true)
	check()
	r, _, seq := b.reserveNoticesForRoot(0)
	check()
	b.rollbackNotices(r)
	check()
	r, _, _ = b.reserveNoticesForRoot(0)
	check()
	b.consumeNotices(r)
	check()
	b.markRootObserved(seq)
	check()
	b.setReviewing(true)
	check()
	b.setReviewing(false)
	check()
	stable := s.ActivitySnapshot()
	if stable.Revision != s.ActivitySnapshot().Revision {
		t.Fatal("read mutated revision")
	}
	b.mu.Lock()
	b.internalErr = errors.New("invalid ledger")
	b.publishUnlock()
	if got := s.ActivitySnapshot(); got.Known || got.ReadyAllowed {
		t.Fatalf("internal error authorized readiness: %+v", got)
	}
}
