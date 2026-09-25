package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/cogito"
	openai "github.com/sashabaranov/go-openai"
)

// TestContextNudgeFiresAfterCompaction verifies that after a successful
// end-of-turn compaction, contextNudgePending is set, and calling
// maybeInjectContextNudge adds a user-role message to the fragment telling
// the model to re-read the detected context files.
func TestContextNudgeFiresAfterCompaction(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# test"), 0644); err != nil {
		t.Fatal(err)
	}

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
	s.workingDir = dir

	before, after, err := s.CompactHistory()
	if err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}
	if before == after {
		t.Fatal("compaction should have changed token count")
	}

	// Simulate what SendMessage does after compaction succeeds.
	s.historyMu.Lock()
	s.contextNudgePending = true
	s.historyMu.Unlock()

	fragLenBefore := len(s.fragment.Messages)
	s.maybeInjectContextNudge()

	if len(s.fragment.Messages) <= fragLenBefore {
		t.Fatal("expected a nudge message to be added to the fragment")
	}
	last := s.fragment.Messages[len(s.fragment.Messages)-1]
	if last.Role != "user" {
		t.Fatalf("nudge role = %q, want user", last.Role)
	}
	if !strings.Contains(last.Content, "AGENTS.md") {
		t.Fatalf("nudge should mention AGENTS.md, got: %q", last.Content)
	}

	// Nudge should only fire once.
	s.historyMu.Lock()
	if s.contextNudgePending {
		t.Fatal("contextNudgePending should be false after nudge")
	}
	s.historyMu.Unlock()
}

// TestContextNudgeSkippedWhenNoContextFiles verifies that the nudge does not
// fire when no project instruction files are detected in the working directory.
func TestContextNudgeSkippedWhenNoContextFiles(t *testing.T) {
	dir := t.TempDir() // empty dir, no AGENTS.md

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
	s.workingDir = dir

	s.historyMu.Lock()
	s.contextNudgePending = true
	s.historyMu.Unlock()

	fragLenBefore := len(s.fragment.Messages)
	s.maybeInjectContextNudge()

	if len(s.fragment.Messages) != fragLenBefore {
		t.Fatal("no nudge should be added when no context files exist")
	}

	s.historyMu.Lock()
	if s.contextNudgePending {
		t.Fatal("contextNudgePending should be cleared even when skipping")
	}
	s.historyMu.Unlock()
}

// TestTrackContextFileReadClearsNudge verifies that calling
// trackContextFileRead with a read tool call on AGENTS.md clears the
// nudge flag, so no nudge fires if the model re-reads the file on its own.
func TestTrackContextFileReadClearsNudge(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# test"), 0644); err != nil {
		t.Fatal(err)
	}

	s := &Session{
		ctx:        context.Background(),
		workingDir: dir,
	}
	s.fragment = cogito.NewFragment()

	s.historyMu.Lock()
	s.contextNudgePending = true
	s.historyMu.Unlock()

	s.trackContextFileRead("read", `{"path":"AGENTS.md"}`)

	s.historyMu.Lock()
	pending := s.contextNudgePending
	s.historyMu.Unlock()
	if pending {
		t.Fatal("contextNudgePending should be false after reading AGENTS.md")
	}

	// Nudge should not fire now.
	fragLenBefore := len(s.fragment.Messages)
	s.maybeInjectContextNudge()
	if len(s.fragment.Messages) != fragLenBefore {
		t.Fatal("no nudge should fire after context file was read")
	}
}

// TestTrackContextFileReadIgnoresNonContextFiles verifies that reading a
// non-context file does not clear the nudge flag.
func TestTrackContextFileReadIgnoresNonContextFiles(t *testing.T) {
	s := &Session{ctx: context.Background()}
	s.fragment = cogito.NewFragment()

	s.historyMu.Lock()
	s.contextNudgePending = true
	s.historyMu.Unlock()

	s.trackContextFileRead("read", `{"path":"main.go"}`)

	s.historyMu.Lock()
	pending := s.contextNudgePending
	s.historyMu.Unlock()
	if !pending {
		t.Fatal("contextNudgePending should still be true after reading non-context file")
	}
}

// TestTrackContextFileReadIgnoresNonReadTool verifies that non-read tool
// calls do not clear the nudge flag.
func TestTrackContextFileReadIgnoresNonReadTool(t *testing.T) {
	s := &Session{ctx: context.Background()}
	s.fragment = cogito.NewFragment()

	s.historyMu.Lock()
	s.contextNudgePending = true
	s.historyMu.Unlock()

	s.trackContextFileRead("bash", `{"path":"AGENTS.md"}`)

	s.historyMu.Lock()
	pending := s.contextNudgePending
	s.historyMu.Unlock()
	if !pending {
		t.Fatal("contextNudgePending should still be true for non-read tool")
	}
}
