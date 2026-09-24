package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/tui/render/inline"
	"github.com/mudler/nib/types"
)

func TestSlowMCPWarningVisibleBeforeSessionReady(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverT, clientT := mcp.NewInMemoryTransports()
	peer, err := serverT.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	m := NewModel(ctx, types.Config{BaseDir: t.TempDir(), Model: "test-model", LogLevel: "error"}, 0, nil, inline.New(), clientT)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(Model)
	done := make(chan tea.Msg, 1)
	initSession := m.initSession()
	go func() { done <- initSession() }()
	warning := make(chan tea.Msg, 1)
	listen := m.listenSlowMCP()
	go func() { warning <- listen() }()
	select {
	case msg := <-warning:
		if _, ok := msg.(mcpSlowMsg); !ok {
			t.Fatalf("got %T, want startup warning", msg)
		}
		next, _ = m.Update(msg)
		m = next.(Model)
		if m.sessionReady {
			t.Fatal("test must warn while startup is pending")
		}
		view := m.View()
		if !strings.Contains(view, "taking a long time") || !strings.Contains(view, "built-in 1") {
			t.Fatalf("warning missing from startup screen: %s", view)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no startup warning before connection finishes")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("startup did not cancel")
	}
}

func TestSlowMCPWarningsKeepEachServerName(t *testing.T) {
	m := frameModel()
	m.sessionReady = false
	m.boot = newBootState()
	for _, name := range []string{"server-a", "server-b"} {
		next, _ := m.Update(mcpSlowMsg(name))
		m = next.(Model)
	}
	view := m.View()
	for _, name := range []string{"server-a", "server-b"} {
		if !strings.Contains(view, name) {
			t.Fatalf("lost warning for %s: %s", name, view)
		}
	}
}
