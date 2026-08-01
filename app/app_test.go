package app

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestVersionReturnsZero(t *testing.T) {
	var out bytes.Buffer
	o := Options{Args: []string{"--version"}, Stdout: &out}
	if code := run(o); code != 0 {
		t.Fatalf("--version exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "nib") {
		t.Fatalf("--version output = %q, want it to mention nib", out.String())
	}
}

// "nib" is the default, so asserting on it alone would pass even if
// ProgramName were ignored. A distinct name is what actually exercises it.
func TestVersionUsesProgramName(t *testing.T) {
	var out bytes.Buffer
	o := Options{Args: []string{"--version"}, Stdout: &out, ProgramName: "local-ai chat"}
	if code := run(o); code != 0 {
		t.Fatalf("--version exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "local-ai chat") {
		t.Fatalf("--version output = %q, want it to use ProgramName", out.String())
	}
}

// The init scripts hardcode "nib" and cmd.GetInitScript takes no program name,
// so this covers only that a known shell yields a script on stdout.
func TestInitEmitsScriptForKnownShell(t *testing.T) {
	var out bytes.Buffer
	o := Options{Args: []string{"--init", "zsh"}, Stdout: &out}
	if code := run(o); code != 0 {
		t.Fatalf("--init zsh exit code = %d, want 0", code)
	}
	if out.Len() == 0 {
		t.Fatal("--init zsh wrote nothing")
	}
}

func TestUnknownShellIsAnError(t *testing.T) {
	var errOut bytes.Buffer
	o := Options{Args: []string{"--init", "tcsh"}, Stderr: &errOut}
	if code := run(o); code == 0 {
		t.Fatal("--init tcsh exit code = 0, want non-zero")
	}
	if !strings.Contains(errOut.String(), "tcsh") {
		t.Fatalf("stderr = %q, want it to name the bad shell", errOut.String())
	}
}

// The pre-move code parsed into the global flag.CommandLine, whose ExitOnError
// mode exits 0 for -h and 2 for anything else. The explicit FlagSet is
// ContinueOnError, so both codes are ours to preserve.
func TestHelpExitsZero(t *testing.T) {
	var out bytes.Buffer
	o := Options{Args: []string{"-h"}, Stdout: &out, Stderr: &out}
	if code := run(o); code != 0 {
		t.Fatalf("-h exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "-version") {
		t.Fatalf("-h output = %q, want the usage text", out.String())
	}
}

func TestUnknownFlagExitsTwo(t *testing.T) {
	var out bytes.Buffer
	o := Options{Args: []string{"--bogus"}, Stdout: &out, Stderr: &out}
	if code := run(o); code != 2 {
		t.Fatalf("--bogus exit code = %d, want 2", code)
	}
}

// run installs a signal handler per call. As library code it has to tear that
// handler down again, or every call leaks a goroutine parked on <-sigs.
func TestRunDoesNotLeakSignalGoroutines(t *testing.T) {
	var out bytes.Buffer
	run(Options{Args: []string{"--version"}, Stdout: &out}) // warm up lazily-started goroutines

	before := runtime.NumGoroutine()
	for range 50 {
		out.Reset()
		if code := run(Options{Args: []string{"--version"}, Stdout: &out}); code != 0 {
			t.Fatalf("--version exit code = %d, want 0", code)
		}
	}

	// The handler goroutines unwind asynchronously, so give them a moment.
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := runtime.NumGoroutine()
		if got <= before+5 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutines after 50 runs = %d, was %d before: run is leaking them", got, before)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Usage and parse errors must land on the configured stderr, never on the
// process's own, or an embedder's output gets polluted.
func TestFlagErrorsGoToConfiguredStderr(t *testing.T) {
	var out, errOut bytes.Buffer
	o := Options{Args: []string{"--bogus"}, Stdout: &out, Stderr: &errOut}
	if code := run(o); code != 2 {
		t.Fatalf("--bogus exit code = %d, want 2", code)
	}
	if errOut.Len() == 0 {
		t.Fatal("--bogus wrote nothing to the configured stderr")
	}
	if out.Len() != 0 {
		t.Fatalf("--bogus wrote %q to stdout, want nothing", out.String())
	}
}
