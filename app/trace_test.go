package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

// Asking for a trace and silently getting none was the reported bug. An
// explicit request that cannot be honored must stop the run, not downgrade it.
func TestUnwritableTraceDirExitsNonZero(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	var errOut bytes.Buffer
	o := Options{
		BaseDir:     t.TempDir(),
		Args:        []string{"--cli", "--trace-dir", filepath.Join(parent, "trace")},
		Defaults:    types.Config{Model: "m", BaseURL: "http://127.0.0.1:1/v1"},
		SkipSetup:   true,
		SkipBareEnv: true,
		Stdin:       strings.NewReader(""),
		Stdout:      &bytes.Buffer{},
		Stderr:      &errOut,
	}
	if code := runCtx(context.Background(), o); code != 1 {
		t.Fatalf("exit code = %d, want 1: an unwritable trace dir must fail the run", code)
	}
	if !strings.Contains(errOut.String(), "cannot write trace to") {
		t.Fatalf("no explanation on stderr, which is the actual bug: %q", errOut.String())
	}
}

// The env var is the form the reporter used, and it must behave identically to
// the flag.
func TestUnwritableTraceDirFromEnvExitsNonZero(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	t.Setenv("NIB_TRACE_DIR", filepath.Join(parent, "trace"))

	var errOut bytes.Buffer
	o := Options{
		BaseDir:     t.TempDir(),
		Args:        []string{"--cli"},
		Defaults:    types.Config{Model: "m", BaseURL: "http://127.0.0.1:1/v1"},
		SkipSetup:   true,
		SkipBareEnv: true,
		Stdin:       strings.NewReader(""),
		Stdout:      &bytes.Buffer{},
		Stderr:      &errOut,
	}
	if code := runCtx(context.Background(), o); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

// A writable trace dir must not be disturbed by the new check: the run proceeds
// and the transcript file exists.
func TestWritableTraceDirIsAccepted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "trace")

	var errOut bytes.Buffer
	o := Options{
		BaseDir:     t.TempDir(),
		Args:        []string{"--cli", "--trace-dir", dir},
		Defaults:    types.Config{Model: "m", BaseURL: "http://127.0.0.1:1/v1"},
		SkipSetup:   true,
		SkipBareEnv: true,
		Stdin:       strings.NewReader(""),
		Stdout:      &bytes.Buffer{},
		Stderr:      &errOut,
	}
	runCtx(context.Background(), o)

	if _, err := os.Stat(filepath.Join(dir, "trace.ndjson")); err != nil {
		t.Fatalf("a writable trace dir produced no transcript: %v", err)
	}
}
