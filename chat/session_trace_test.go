package chat

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/nib/types"
)

func TestNewSessionEnablesTracing(t *testing.T) {
	dir := t.TempDir()
	cfg := types.Config{Model: "test-model", TraceDir: dir}

	sess, err := NewSession(context.Background(), cfg, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	if _, err := os.Stat(filepath.Join(dir, "trace.ndjson")); err != nil {
		t.Fatalf("trace file not created when TraceDir set: %v", err)
	}
}

func TestNewSessionTracingDisabledByDefault(t *testing.T) {
	dir := t.TempDir()
	cfg := types.Config{Model: "test-model"} // no TraceDir

	sess, err := NewSession(context.Background(), cfg, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	if _, err := os.Stat(filepath.Join(dir, "trace.ndjson")); !os.IsNotExist(err) {
		t.Fatalf("trace file should not exist when TraceDir unset, stat err = %v", err)
	}
}
