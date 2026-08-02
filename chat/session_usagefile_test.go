package chat

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/types"
)

// Close is the only place the spend becomes durable: in tmux-widget mode the
// pane carrying the printed summary is gone before anyone can read it.
func TestCloseWritesUsageJSON(t *testing.T) {
	dir := t.TempDir()
	sess, err := NewSession(context.Background(), types.Config{Model: "test-model", TraceDir: dir}, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sess.addUsage(cogito.LLMUsage{PromptTokens: 300, CompletionTokens: 800, TotalTokens: 1100})
	sess.countTurn()

	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "usage.json"))
	if err != nil {
		t.Fatalf("usage.json missing after Close: %v", err)
	}
	var got SessionUsage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("usage.json is not valid JSON: %v (%q)", err, data)
	}
	want := SessionUsage{PromptTokens: 300, CompletionTokens: 800, TotalTokens: 1100, Turns: 1}
	if got != want {
		t.Fatalf("usage.json = %+v, want %+v", got, want)
	}
}

// Nothing was asked for, so nothing is written — an untraced session must not
// litter the working directory.
//
// t.Chdir is what gives this test teeth: without the `if s.traceDir != ""`
// guard, filepath.Join("", "usage.json") is a bare relative path and Close
// writes into the process's working directory — which, for `go test`, is the
// package source dir. Asserting on an unrelated temp dir could never catch
// that; running IN the temp dir does.
func TestCloseWritesNoUsageJSONWithoutTraceDir(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	sess, err := NewSession(context.Background(), types.Config{Model: "test-model"}, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "usage.json")); !os.IsNotExist(err) {
		t.Fatalf("usage.json should not exist without TraceDir, stat err = %v", err)
	}
}

// Close is not guaranteed to run once — a caller may defer it and also call it
// on a shutdown path. The second run must overwrite, not corrupt or panic.
func TestCloseTwiceLeavesOneUsageReport(t *testing.T) {
	dir := t.TempDir()
	sess, err := NewSession(context.Background(), types.Config{Model: "test-model", TraceDir: dir}, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sess.countTurn()
	_ = sess.Close()
	_ = sess.Close()

	data, err := os.ReadFile(filepath.Join(dir, "usage.json"))
	if err != nil {
		t.Fatalf("usage.json missing: %v", err)
	}
	var got SessionUsage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("usage.json is not a single JSON object after two Closes: %v (%q)", err, data)
	}
	if got.Turns != 1 {
		t.Fatalf("turns = %d, want 1", got.Turns)
	}
}
