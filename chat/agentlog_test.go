package chat

import (
	"strconv"
	"strings"
	"testing"

	"github.com/mudler/cogito"
)

func TestAgentLogStoreRecordAndDump(t *testing.T) {
	s := newAgentLogStore()
	s.recordCall("agent-1", &cogito.ToolChoice{
		Name: "bash", ID: "call-1", Arguments: map[string]any{"script": "echo hi"},
	})
	s.recordResult(cogito.ToolStatus{
		Name: "bash", ToolArguments: cogito.ToolChoice{ID: "call-1"}, Result: "hi\n",
	})

	out := s.dump("agent-1")
	if !strings.Contains(out, "→ bash(") {
		t.Fatalf("missing tool-call line:\n%s", out)
	}
	if !strings.Contains(out, "← bash: hi") {
		t.Fatalf("missing correlated result line:\n%s", out)
	}

	if s.dump("nope") != "" {
		t.Fatal("unknown agent should have an empty log")
	}
	// A result for an unknown tool id is dropped, not attributed or panicking.
	s.recordResult(cogito.ToolStatus{Name: "x", ToolArguments: cogito.ToolChoice{ID: "??"}, Result: "r"})
	// Empty agent id is a no-op.
	s.recordCall("", &cogito.ToolChoice{Name: "noop"})
}

func TestAgentLogStoreAgentFor(t *testing.T) {
	s := newAgentLogStore()
	s.recordCall("agent-7", &cogito.ToolChoice{Name: "bash", ID: "call-7"})

	if got := s.agentFor("call-7"); got != "agent-7" {
		t.Fatalf("agentFor(known id) = %q, want %q", got, "agent-7")
	}
	if got := s.agentFor("call-unknown"); got != "" {
		t.Fatalf("agentFor(unknown id) = %q, want empty (root-agent call)", got)
	}
}

func TestAgentLogStoreCaps(t *testing.T) {
	s := newAgentLogStore()
	for i := 0; i < agentLogLimit+25; i++ {
		s.recordCall("a", &cogito.ToolChoice{Name: "t" + strconv.Itoa(i)})
	}
	lines := strings.Split(s.dump("a"), "\n")
	if len(lines) != agentLogLimit {
		t.Fatalf("log should be capped at %d lines, got %d", agentLogLimit, len(lines))
	}
	// Oldest dropped, newest kept.
	if !strings.Contains(s.dump("a"), "t"+strconv.Itoa(agentLogLimit+24)) {
		t.Fatal("newest entry should be retained")
	}
}
