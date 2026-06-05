package chat

import (
	"context"
	"testing"

	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/types"
)

func TestInjectRequiresLiveRun(t *testing.T) {
	s, err := NewSession(context.Background(), types.Config{}, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	// No live run: Inject is a no-op and reports false.
	if s.Inject("hello") {
		t.Fatal("Inject should return false when no run is live")
	}

	// Empty/whitespace input is always rejected.
	if s.Inject("   ") {
		t.Fatal("Inject should reject blank input")
	}

	// With a live run it succeeds and delivers the message into the channel.
	s.runMu.Lock()
	s.runLive = true
	s.runMu.Unlock()
	if !s.Inject("hello") {
		t.Fatal("Inject should succeed against a live run")
	}
	select {
	case msg := <-s.inject:
		if msg.Content != "hello" || msg.Role != "user" {
			t.Fatalf("injected message = %+v, want user/hello", msg)
		}
	default:
		t.Fatal("expected the injected message on the channel")
	}
}

func TestSetShellJobsNilSafe(t *testing.T) {
	s, err := NewSession(context.Background(), types.Config{}, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	// nil registry must not panic and leaves the pending-work predicate harmless.
	s.SetShellJobs(nil)

	// A real registry wires the completion hook; with no live run the injected
	// notice is simply dropped (Inject returns false), which must not panic.
	jobs := wizmcp.NewShellJobs()
	s.SetShellJobs(jobs)
	if s.shellJobs == nil {
		t.Fatal("SetShellJobs should store the registry")
	}
}
