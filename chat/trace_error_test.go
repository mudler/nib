package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

// A recorder failure used to be an xlog.Warn, and the default log level is
// "error", so the warning was discarded and the session ran untraced in
// silence. NewSession must refuse instead.
func TestNewSessionFailsWhenTraceDirIsUnwritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	cfg := types.Config{
		Model:    "m",
		BaseURL:  "http://127.0.0.1:1/v1",
		TraceDir: filepath.Join(parent, "trace"),
	}
	_, err := NewSession(context.Background(), cfg, Callbacks{})
	if err == nil {
		t.Fatal("NewSession accepted an unwritable trace dir; tracing would be silently off")
	}
	if !strings.Contains(err.Error(), "trace") {
		t.Fatalf("error does not name tracing as the cause: %v", err)
	}
}

// The control: no trace dir configured is not an error.
func TestNewSessionWithoutTraceDirSucceeds(t *testing.T) {
	cfg := types.Config{Model: "m", BaseURL: "http://127.0.0.1:1/v1"}
	s, err := NewSession(context.Background(), cfg, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession without tracing: %v", err)
	}
	s.Close()
}
