package chat

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mudler/nib/types"
)

func TestApprovalConcurrentSessionGrants(t *testing.T) {
	for _, prefix := range []bool{false, true} {
		t.Run(fmt.Sprint(prefix), func(t *testing.T) {
			arrived := make(chan struct{}, 2)
			release := make(chan struct{})
			s := newDecideSession("allowlist", func(req ToolCallRequest) ToolCallResponse {
				arrived <- struct{}{}
				<-release
				resp := ToolCallResponse{Approved: true, AlwaysAllow: true}
				if prefix {
					resp.AlwaysPrefix = "git"
				}
				return resp
			})
			var wg sync.WaitGroup
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					s.ToolCallDenied(ToolCallRequest{Name: "bash", Arguments: `{"script":"git status"}`})
				}()
			}
			<-arrived
			<-arrived
			close(release)
			wg.Wait()
			if s.ToolCallDenied(ToolCallRequest{Name: "bash", Arguments: `{"script":"git diff"}`}) {
				t.Fatal("session grant lost")
			}
		})
	}
}

func TestApprovalCallbackReentry(t *testing.T) {
	var s *Session
	s = newDecideSession("allowlist", func(req ToolCallRequest) ToolCallResponse {
		if req.Name == "outer" && s.ToolCallDenied(ToolCallRequest{Name: "inner"}) {
			t.Error("inner denied")
		}
		return ToolCallResponse{Approved: true, AlwaysAllow: true}
	})
	done := make(chan struct{})
	go func() { s.ToolCallDenied(ToolCallRequest{Name: "outer"}); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("approval callback deadlocked on re-entry")
	}
}

// Use the real SendMessage -> Cogito background child callback, not a simulated
// AgentID on the public API. Grant B only after A's foreground turn has ended.
func TestApprovalDetachedTurnIsolation(t *testing.T) {
	backend := &agentInputBackend{subWaiting: make(chan struct{}), subGo: make(chan struct{})}
	server := httptest.NewServer(backend)
	defer server.Close()
	var release sync.Once
	defer release.Do(func() { close(backend.subGo) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	parked := make(chan struct{}, 4)
	completed := make(chan struct{}, 1)
	asked := make(chan ToolCallRequest, 4)
	s, err := NewSession(ctx, types.Config{
		Model: "fake-model", APIKey: "fake-key", BaseURL: server.URL + "/v1", ApprovalMode: "allowlist", WorkingDir: t.TempDir(),
		BuiltinTools: []string{"spawn_agent", "agent_logs"},
		AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 1},
	}, Callbacks{
		OnToolCall: func(req ToolCallRequest) ToolCallResponse {
			if req.Name == "grant-b" {
				return ToolCallResponse{Approved: true, AllowAllTurn: true}
			}
			if req.AgentID != "" {
				asked <- req
			}
			return ToolCallResponse{Approved: true}
		},
		OnParked: func(string) { parked <- struct{}{} },
		OnAgentEvent: func(ev AgentEvent) {
			if ev.Status == AgentStatusCompleted {
				completed <- struct{}{}
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	defer release.Do(func() { close(backend.subGo) })
	wait := func(ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-ctx.Done():
			t.Fatal("timed out")
		}
	}
	done := make(chan struct{}, 2)
	go func() { s.SendMessage("start"); done <- struct{}{} }()
	wait(backend.subWaiting)
	wait(parked)
	s.Interrupt()
	wait(done)
	go func() { s.SendMessage("next turn"); done <- struct{}{} }()
	wait(parked)
	if s.ToolCallDenied(ToolCallRequest{Name: "grant-b"}) {
		t.Fatal("grant denied")
	}
	release.Do(func() { close(backend.subGo) })
	wait(completed)
	wait(done)
	select {
	case req := <-asked:
		if req.Name != "agent_logs" {
			t.Fatalf("unexpected request: %+v", req)
		}
	default:
		t.Fatal("detached turn A inherited turn B's grant; approval callback bypassed")
	}
}

// A prompt can remain open across a turn boundary. Its answer must mint a
// grant for the request's captured turn, not whichever turn is now foreground.
func TestApprovalLateTurnGrant(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	s := newDecideSession("allowlist", func(req ToolCallRequest) ToolCallResponse {
		if req.Name == "late" {
			close(entered)
			<-release
			return ToolCallResponse{Approved: true, AllowAllTurn: true}
		}
		return ToolCallResponse{Approved: false}
	})
	a := s.currentApprovalTurn()
	done := make(chan struct{})
	go func() { s.ToolCallDenied(ToolCallRequest{Name: "late"}); close(done) }()
	<-entered
	b := s.newApprovalTurn()
	close(release)
	<-done
	req := ToolCallRequest{Name: "other"}
	if !s.decideToolCallForTurn(req, a).Approved {
		t.Fatal("old turn lost its own grant")
	}
	if s.decideToolCallForTurn(req, b).Approved || !s.ToolCallDenied(req) {
		t.Fatal("late approval leaked into current turn")
	}
}

func TestApprovalSessionGrantsSpanTurns(t *testing.T) {
	for _, prefix := range []bool{false, true} {
		t.Run(fmt.Sprint(prefix), func(t *testing.T) {
			s := newDecideSession("allowlist", func(req ToolCallRequest) ToolCallResponse {
				resp := ToolCallResponse{Approved: true, AlwaysAllow: true}
				if prefix {
					resp.AlwaysPrefix = "git"
				}
				return resp
			})
			a := s.currentApprovalTurn()
			b := s.newApprovalTurn()
			req := ToolCallRequest{Name: "bash", Arguments: `{"script":"git status"}`}
			s.decideToolCallForTurn(req, b)
			s.callbacks.OnToolCall = func(ToolCallRequest) ToolCallResponse { t.Error("session grant not shared"); return ToolCallResponse{} }
			if !s.decideToolCallForTurn(req, a).Approved {
				t.Fatal("old turn cannot use session grant")
			}
		})
	}
}

func TestApprovalConcurrentTurnGrants(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	s := newDecideSession("allowlist", func(ToolCallRequest) ToolCallResponse {
		entered <- struct{}{}
		<-release
		return ToolCallResponse{Approved: true, AllowAllTurn: true}
	})
	a := s.currentApprovalTurn()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.decideToolCallForTurn(ToolCallRequest{Name: "grant"}, a)
		}()
	}
	<-entered
	<-entered
	b := s.newApprovalTurn()
	close(release)
	wg.Wait()
	s.grantsMu.Lock()
	defer s.grantsMu.Unlock()
	if !a.allowAll || b.allowAll {
		t.Fatal("concurrent answers did not stay in their turn")
	}
}
