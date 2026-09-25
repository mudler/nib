package chat

import (
	"strings"
	"testing"

	"github.com/mudler/nib/mcp"
	openai "github.com/sashabaranov/go-openai"
)

// TestCompactHistorySavesArtifact verifies that compactHistory saves the full
// head as an artifact when DisableArtifactSpill is false and an artifact store
// is present. The compaction notice must contain the artifact URI.
func TestCompactHistorySavesArtifact(t *testing.T) {
	frag := []openai.ChatCompletionMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u3"},
		{Role: "assistant", Content: "a3"},
	}
	llm := &fakeSummaryLLM{reply: "SUMMARY"}
	s := newCompactTestSession(llm, 2, frag, frag)
	s.compaction.MaxContextTokens = 1 << 20
	s.artifacts = mcp.NewArtifactStore()

	if _, _, err := s.CompactHistory(); err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}

	if s.artifacts.Count() != 1 {
		t.Fatalf("expected 1 artifact after compaction, got %d", s.artifacts.Count())
	}
	art := s.artifacts.Get(1)
	if art == nil {
		t.Fatal("artifact 1 not found")
	}
	if !strings.Contains(art.Content, "u1") || !strings.Contains(art.Content, "a1") {
		t.Errorf("artifact should contain head messages, got: %q", art.Content)
	}
	// The compaction notice must mention the artifact URI.
	if !strings.Contains(s.fragment.Messages[0].Content, "artifact://1") {
		t.Errorf("compaction notice should mention artifact://1, got: %q", s.fragment.Messages[0].Content)
	}
	if !strings.Contains(s.fragment.Messages[0].Content, "search_artifacts") {
		t.Errorf("compaction notice should mention search_artifacts, got: %q", s.fragment.Messages[0].Content)
	}
}

// TestCompactHistoryArtifactSpillDisabled verifies that no artifact is saved
// when DisableArtifactSpill is true, and the compaction notice does not
// mention an artifact URI.
func TestCompactHistoryArtifactSpillDisabled(t *testing.T) {
	frag := []openai.ChatCompletionMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u3"},
		{Role: "assistant", Content: "a3"},
	}
	llm := &fakeSummaryLLM{reply: "SUMMARY"}
	s := newCompactTestSession(llm, 2, frag, frag)
	s.compaction.MaxContextTokens = 1 << 20
	s.compaction.DisableArtifactSpill = true
	s.artifacts = mcp.NewArtifactStore()

	if _, _, err := s.CompactHistory(); err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}

	if s.artifacts.Count() != 0 {
		t.Fatalf("expected 0 artifacts when disabled, got %d", s.artifacts.Count())
	}
	if strings.Contains(s.fragment.Messages[0].Content, "artifact://") {
		t.Errorf("compaction notice should not mention artifact URI when disabled, got: %q", s.fragment.Messages[0].Content)
	}
}

// TestCompactHistoryNoArtifactStore verifies that compaction still succeeds
// when no artifact store is present (e.g. session without one), and no panic.
func TestCompactHistoryNoArtifactStore(t *testing.T) {
	frag := []openai.ChatCompletionMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u3"},
		{Role: "assistant", Content: "a3"},
	}
	llm := &fakeSummaryLLM{reply: "SUMMARY"}
	s := newCompactTestSession(llm, 2, frag, frag)
	s.compaction.MaxContextTokens = 1 << 20
	// s.artifacts is nil — no store

	if _, _, err := s.CompactHistory(); err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}
	if strings.Contains(s.fragment.Messages[0].Content, "artifact://") {
		t.Errorf("compaction notice should not mention artifact URI when no store, got: %q", s.fragment.Messages[0].Content)
	}
}
