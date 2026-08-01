package app

import (
	"bytes"
	"context"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mudler/nib/types"
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

// The default name's script is covered byte for byte in cmd; this covers only
// that a known shell yields a script on stdout.
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

// ProgramName has to reach the emitted widget, for every shell. An embedder's
// users do not have a `nib` on their PATH, so a script that invokes one is a
// keybinding that fails with "command not found" the first time it is pressed.
//
// The assertion is that the standalone name survives nowhere in the script:
// every mention of it is either the command the widget runs, the function name,
// or prose telling the reader what they installed, and all three are wrong when
// they name a binary the reader does not have.
func TestInitScriptUsesProgramName(t *testing.T) {
	for _, shell := range []string{"zsh", "bash", "fish"} {
		t.Run(shell, func(t *testing.T) {
			var out bytes.Buffer
			o := Options{Args: []string{"--init", shell}, Stdout: &out, ProgramName: "local-ai chat"}
			if code := run(o); code != 0 {
				t.Fatalf("--init %s exit code = %d, want 0", shell, code)
			}
			script := out.String()
			if !strings.Contains(script, "local-ai chat --height 50%") {
				t.Fatalf("--init %s does not invoke the embedder's command:\n%s", shell, script)
			}
			if strings.Contains(script, "nib") {
				t.Fatalf("--init %s still names the standalone binary:\n%s", shell, script)
			}
		})
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

// skipSetupOptions is the one situation the setup gate fires in: an empty
// injected root, the bare environment suppressed, and a stdin that is not a
// terminal, so decideSetup lands on setupAbort rather than the wizard.
//
// --cli is what makes "the gate was skipped" observable. --version returns
// before the config load, so it never reaches the gate at all and would pass
// with SkipSetup ignored. The CLI loop, handed an already-cancelled context,
// returns context.Canceled on its first iteration without reading stdin or
// talking to a model, which gives the far side of the gate a cheap, terminating
// exit.
func skipSetupOptions(t *testing.T, out, errOut io.Writer) Options {
	t.Helper()
	t.Setenv("MODEL", "")
	t.Setenv("API_KEY", "")
	t.Setenv("BASE_URL", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return Options{
		Args:        []string{"--cli"},
		BaseDir:     t.TempDir(), // empty: no model configured anywhere
		SkipBareEnv: true,
		Stdin:       strings.NewReader(""),
		Stdout:      out,
		Stderr:      errOut,
	}
}

func cancelledContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	// cancel is idempotent and the parent is Background, so the immediate call
	// releases everything: no t.Cleanup is needed to avoid a leak.
	cancel()
	return ctx
}

// The exit code the documented one-shot idiom depends on. Piping a question
// into `--cli` used to answer it and then exit 1 with "Error: EOF", because
// stdin running out came back through the CLI loop as a failure, so every
// script wrapping nib saw a false failure on every successful run.
//
// This is the far side of the setup gate rather than a unit of the loop: the
// exit code is what the shell sees, and it is what was wrong.
func TestPipedStdinExitsZero(t *testing.T) {
	var errOut bytes.Buffer
	o := skipSetupOptions(t, io.Discard, &errOut)
	o.SkipSetup = true
	o.Stdin = strings.NewReader("help\n")

	if code := runCtx(context.Background(), o); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, errOut.String())
	}
}

// The control: with SkipSetup off, standalone nib's abort must be untouched.
func TestSetupAbortStillFiresWithoutSkipSetup(t *testing.T) {
	var errOut bytes.Buffer
	o := skipSetupOptions(t, io.Discard, &errOut)
	if code := runCtx(cancelledContext(t), o); code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr = %q", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "no model configured") {
		t.Fatalf("stderr = %q, want the no-model abort", errOut.String())
	}
}

func TestSkipSetupSuppressesTheWizardAbort(t *testing.T) {
	var errOut bytes.Buffer
	o := skipSetupOptions(t, io.Discard, &errOut)
	o.SkipSetup = true

	runCtx(cancelledContext(t), o)

	if strings.Contains(errOut.String(), "no model configured") {
		t.Fatalf("SkipSetup did not suppress the setup abort: %q", errOut.String())
	}
	if !strings.Contains(errOut.String(), "context canceled") {
		t.Fatalf("stderr = %q, want the run to have reached the CLI past the gate", errOut.String())
	}
}

// Options.Defaults must reach config.LoadWith. A seeded model satisfies the
// setup gate on its own, which is the whole point for an embedder: no wizard,
// no bare MODEL, just the host's own resolution.
func TestOptionsDefaultsSeedTheLoadedConfig(t *testing.T) {
	var errOut bytes.Buffer
	o := skipSetupOptions(t, io.Discard, &errOut)
	o.Defaults = types.Config{Model: "seeded-model", BaseURL: "http://seed.invalid/v1"}

	runCtx(cancelledContext(t), o)

	if strings.Contains(errOut.String(), "no model configured") {
		t.Fatalf("Options.Defaults never reached the config load: %q", errOut.String())
	}
}

// Options.SkipBareEnv must reach config.LoadWith too: with it set, a bare MODEL
// in the environment must not be enough to get past the gate.
func TestOptionsSkipBareEnvReachesTheConfigLoad(t *testing.T) {
	var errOut bytes.Buffer
	o := skipSetupOptions(t, io.Discard, &errOut)
	t.Setenv("MODEL", "env-model")

	if code := runCtx(cancelledContext(t), o); code != 1 {
		t.Fatalf("exit code = %d, want 1: MODEL must not satisfy the gate under SkipBareEnv", code)
	}
	if !strings.Contains(errOut.String(), "no model configured") {
		t.Fatalf("stderr = %q, want the no-model abort", errOut.String())
	}
}
