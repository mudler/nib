package chat

import (
	"context"
	"net/http/httptest"
	"sync"
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

// A background child survives an interrupted foreground turn. Its completion
// belongs to the spawning turn even after another SendMessage captures a sink.
func TestAgentCallbacksDetachedCompletionRetainsOwner(t *testing.T) {
	backend := &agentInputBackend{subWaiting: make(chan struct{}), subGo: make(chan struct{})}
	var release sync.Once
	defer release.Do(func() { close(backend.subGo) })
	server := httptest.NewServer(backend)
	defer server.Close()
	var captures atomic.Int32
	captured := make(chan int32, 4)
	events := make(chan AgentEvent, 8)
	parked := make(chan struct{}, 8)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, err := NewSession(ctx, types.Config{
		Model: "fake-model", APIKey: "fake-key", BaseURL: server.URL + "/v1", ApprovalMode: "auto", WorkingDir: t.TempDir(),
		AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 1},
	}, Callbacks{
		AgentCallbacks: func() func(AgentEvent) {
			owner := captures.Add(1)
			captured <- owner
			return func(ev AgentEvent) { ev.ObservationScope.Generation = uint64(owner); events <- ev }
		},
		OnParked: func(string) { parked <- struct{}{} },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	defer release.Do(func() { close(backend.subGo) })
	done := make(chan error, 1)
	go func() { _, err := s.SendMessage("start"); done <- err }()
	select {
	case <-backend.subWaiting:
	case <-ctx.Done():
		t.Fatal("child did not start")
	}
	select {
	case <-parked:
	case <-ctx.Done():
		t.Fatal("first turn did not park")
	}
	select {
	case ev := <-events:
		if ev.Status != AgentStatusRunning || ev.ObservationScope.Generation != 1 {
			t.Fatalf("wrong spawn owner: %+v", ev)
		}
	case <-ctx.Done():
		t.Fatal("missing spawn")
	}
	s.Interrupt()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("first turn did not stop")
	}
	go func() { _, err := s.SendMessage("next turn"); done <- err }()
	for _, want := range []int32{1, 2} {
		select {
		case got := <-captured:
			if got != want {
				t.Fatalf("capture %d, want %d", got, want)
			}
		case <-ctx.Done():
			t.Fatal("missing turn capture")
		}
	}
	// Ensure the second turn is active before allowing the old child to finish.
	select {
	case <-parked:
	case <-ctx.Done():
		t.Fatal("second turn did not park")
	}
	release.Do(func() { close(backend.subGo) })
	select {
	case ev := <-events:
		if ev.Status != AgentStatusCompleted || ev.ObservationScope.Generation != 1 {
			t.Fatalf("completion lost spawning owner: %+v", ev)
		}
	case <-ctx.Done():
		t.Fatal("missing detached completion")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("second turn did not finish")
	}
	if captures.Load() != 2 {
		t.Fatalf("factory captures = %d, want 2", captures.Load())
	}
}
