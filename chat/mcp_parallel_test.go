package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/xlog"
)

type gatedMCPTransport struct {
	name         string
	started      chan<- string
	release      <-chan struct{}
	active, peak *atomic.Int32
}

func (t gatedMCPTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	n := t.active.Add(1)
	defer t.active.Add(-1)
	for old := t.peak.Load(); n > old; old = t.peak.Load() {
		if t.peak.CompareAndSwap(old, n) {
			break
		}
	}
	t.started <- t.name
	select {
	case <-t.release:
		return nil, errors.New(t.name)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestMCPBatchLimitsConcurrencyAndPreservesResults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan string, 25)
	release := make(chan struct{})
	defer close(release)
	var active, peak atomic.Int32
	jobs := make([]mcpConnectJob, 23)
	for i := range jobs {
		name := fmt.Sprintf("server-%02d", i)
		jobs[i] = mcpConnectJob{name: name, transport: gatedMCPTransport{name, started, release, &active, &peak}}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	done := make(chan []mcpConnectResult, 1)
	go func() { done <- connectMCPBatch(ctx, client, jobs) }()
	for range 10 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("10 connections did not start in parallel")
		}
	}
	select {
	case name := <-started:
		t.Fatalf("exceeded connection limit: %s", name)
	case <-time.After(30 * time.Millisecond):
	}
	// Free one slot and ensure queued work starts immediately.
	release <- struct{}{}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("queued connection did not start")
	}
	for range 22 {
		release <- struct{}{}
	}
	select {
	case results := <-done:
		for i, r := range results {
			if r.err == nil || r.err.Error() != jobs[i].name {
				t.Fatalf("result %d: %v", i, r.err)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("batch did not finish")
	}
	if peak.Load() != 10 {
		t.Fatalf("peak concurrency=%d, want 10", peak.Load())
	}
}

type warningWriter struct{ lines chan string }

func (w warningWriter) Write(p []byte) (int, error) { w.lines <- string(p); return len(p), nil }

func TestMCPSlowConnectionWarning(t *testing.T) {
	warnings := make(chan string, 1)
	lines := make(chan string, 10)
	xlog.SetLogger(slog.New(slog.NewTextHandler(warningWriter{lines}, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer xlog.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	defer close(release)
	started := make(chan string, 1)
	var active, peak atomic.Int32
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = connectNamedMCP(ctx, client, mcpConnectJob{name: "slow-server", transport: gatedMCPTransport{"slow-server", started, release, &active, &peak}, onSlow: func(name string) { warnings <- name }})
	}()
	<-started
	select {
	case line := <-lines:
		t.Fatalf("warning before one second: %s", line)
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case line := <-lines:
		if !strings.Contains(line, "taking a long time") || !strings.Contains(line, "slow-server") {
			t.Fatalf("wrong warning: %s", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("slow connection was not logged")
	}
	select {
	case name := <-warnings:
		if name != "slow-server" {
			t.Fatalf("wrong UI warning: %q", name)
		}
	case <-time.After(time.Second):
		t.Fatal("slow connection was not reported to UI")
	}
	cancel()
	<-done
}

func TestMCPBatchCancellationSkipsQueuedConnections(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan string, 20)
	release := make(chan struct{})
	defer close(release)
	var active, peak atomic.Int32
	jobs := make([]mcpConnectJob, 20)
	for i := range jobs {
		jobs[i] = mcpConnectJob{name: "cancelled", transport: gatedMCPTransport{"cancelled", started, release, &active, &peak}}
	}
	done := make(chan []mcpConnectResult, 1)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	go func() { done <- connectMCPBatch(ctx, client, jobs) }()
	for range 10 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("workers did not start")
		}
	}
	cancel()
	select {
	case results := <-done:
		for _, r := range results {
			if !errors.Is(r.err, context.Canceled) {
				t.Fatalf("expected cancellation, got %v", r.err)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not stop batch")
	}
	if len(started) != 0 {
		t.Fatal("queued connections started after cancellation")
	}
}
