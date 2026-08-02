package trace

import (
	"os"
	"path/filepath"
	"testing"
)

// The ordinary case: a directory that does not exist yet is created, because
// --trace-dir naming a fresh path is the normal way to start a traced run.
func TestPreflightCreatesMissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "trace")
	if err := Preflight(dir); err != nil {
		t.Fatalf("Preflight on a creatable dir: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("Preflight did not create the dir: %v", err)
	}
}

// Preflight exists to catch this before a session starts. An unwritable parent
// is what a jailed container looks like, which is the reported failure.
func TestPreflightRejectsUnwritableParent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	if err := Preflight(filepath.Join(parent, "trace")); err == nil {
		t.Fatal("Preflight accepted a dir it cannot create; the session would start with tracing silently off")
	}
}

// Appending to an existing transcript is supported: a second run in the same
// trace dir must not be rejected.
func TestPreflightAcceptsExistingTranscript(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Preflight(dir); err != nil {
		t.Fatalf("Preflight rejected an existing transcript: %v", err)
	}
}

// Probing must not truncate a transcript an earlier run wrote.
func TestPreflightPreservesExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)
	if err := os.WriteFile(path, []byte("{\"a\":1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Preflight(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{\"a\":1}\n" {
		t.Fatalf("Preflight truncated the transcript: %q", data)
	}
}
