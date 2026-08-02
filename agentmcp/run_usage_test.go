package agentmcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mudler/nib/types"
)

// The headless MCP path accepts a TraceDir and fails fast on a bad one, so it
// owes the same spend report as the CLI and the TUI. It got none until Run
// closed the session: serve blocks for the server's whole life, so the tear-down
// has to be a defer around it rather than a line after NewSession.
//
// Served over HTTP because the stdio branch would consume the test process's
// own stdin. Nothing connects — the assertion is about shutdown, not traffic.
func TestRunWritesUsageJSONOnShutdown(t *testing.T) {
	dir := t.TempDir()
	cfg := types.Config{Model: "test-model", TraceDir: dir, ApprovalMode: "auto"}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, cfg, Options{HTTP: true, Addr: "127.0.0.1:0"}, nil)
	}()

	// Give the listener a moment to come up so this exercises a real shutdown
	// rather than the already-shutting-down short circuit in ListenAndServe.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}

	data, err := os.ReadFile(filepath.Join(dir, "usage.json"))
	if err != nil {
		t.Fatalf("usage.json missing after the MCP server shut down: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("usage.json is not valid JSON: %v (%q)", err, data)
	}
	if _, ok := got["total_tokens"]; !ok {
		t.Fatalf("usage.json has no total_tokens: %q", data)
	}
}
