package chat

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/nib/types"
)

// Exercise SendMessage's actual Cogito spawn/completion wiring, not just the
// event mapper. Existing OnAgentEvent users keep the fallback path.
func TestAgentCallbacksCapturedBySendMessage(t *testing.T) {
	backend := &agentInputBackend{subWaiting: make(chan struct{}), subGo: make(chan struct{})}
	close(backend.subGo)
	server := httptest.NewServer(backend)
	defer server.Close()
	var captures atomic.Int32
	events := make(chan AgentEvent, 4)
	s, err := NewSession(context.Background(), types.Config{
		Model: "fake-model", APIKey: "fake-key", BaseURL: server.URL + "/v1", ApprovalMode: "auto",
		AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 1},
	}, Callbacks{
		OnAgentEvent: func(AgentEvent) { t.Error("factory did not replace fallback") },
		AgentCallbacks: func() func(AgentEvent) {
			origin := captures.Add(1)
			return func(ev AgentEvent) { ev.ObservationScope.Generation = uint64(origin); events <- ev }
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.SendMessage("start"); err != nil {
		t.Fatal(err)
	}
	for _, status := range []AgentStatus{AgentStatusRunning, AgentStatusCompleted} {
		select {
		case ev := <-events:
			if ev.Status != status || ev.ObservationScope.Generation != 1 {
				t.Fatalf("unexpected captured event: %+v", ev)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("missing agent event")
		}
	}
	if captures.Load() != 1 {
		t.Fatal("factory not captured exactly once")
	}
}
