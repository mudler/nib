package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// Main takes no Options, so what it writes goes to the process streams. These
// helpers swap them for pipes so the exported entrypoint can be exercised for
// real rather than through the unexported run.
func captureProcessOutput(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// A non-terminal stdin keeps the setup gate's TTY probe deterministic, so a
	// bare invocation aborts instead of launching the wizard under a real tty.
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	origOut, origErr, origIn := os.Stdout, os.Stderr, os.Stdin
	os.Stdout, os.Stderr, os.Stdin = outW, errW, inR

	outCh, errCh := make(chan string, 1), make(chan string, 1)
	go func() { b, _ := io.ReadAll(outR); outCh <- string(b) }()
	go func() { b, _ := io.ReadAll(errR); errCh <- string(b) }()

	defer func() {
		os.Stdout, os.Stderr, os.Stdin = origOut, origErr, origIn
		outW.Close()
		errW.Close()
		inW.Close()
		inR.Close()
		stdout, stderr = <-outCh, <-errCh
		outR.Close()
		errR.Close()
	}()

	fn()
	return
}

// emptyRootEnv points the config load at an empty root with no model anywhere,
// so a full run reaches the setup gate and aborts instead of starting a TUI.
// Main takes no BaseDir, so the default XDG resolution is what has to be
// redirected, HOME included: the developer's own ~/.config/nib/config.yaml is
// on that search path.
func emptyRootEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MODEL", "")
	t.Setenv("API_KEY", "")
	t.Setenv("BASE_URL", "")
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
}

// Main is handed a full argv and must drop the program name. Passing it
// through unsliced would leave "nib" as the first token, flag parsing would
// stop at it, and --version would never be seen.
func TestMainDropsTheProgramNameFromArgv(t *testing.T) {
	emptyRootEnv(t)

	var code int
	out, _ := captureProcessOutput(t, func() {
		code = Main([]string{"nib", "--version"})
	})
	if code != 0 {
		t.Fatalf("Main(argv) exit = %d, want 0: argv[0] was not dropped", code)
	}
	if !strings.Contains(out, "nib") {
		t.Fatalf("stdout = %q, want the version line", out)
	}
}

// The program name is dropped whatever it is: an embedder's own argv[0] must
// not be parsed as a nib argument either.
func TestMainDropsAnyProgramName(t *testing.T) {
	emptyRootEnv(t)

	var code int
	out, _ := captureProcessOutput(t, func() {
		code = Main([]string{"/usr/local/bin/local-ai", "--version"})
	})
	if code != 0 {
		t.Fatalf("Main(argv) exit = %d, want 0", code)
	}
	if !strings.Contains(out, "nib") {
		t.Fatalf("stdout = %q, want the version line", out)
	}
}

// An argv with no program name at all, and one with nothing after it, must not
// index off the end. Both mean "no arguments", which is nib's bare invocation:
// with an empty root and no terminal that lands on the setup abort.
func TestMainAcceptsAnEmptyArgv(t *testing.T) {
	cases := map[string][]string{"nil": nil, "empty": {}, "program name only": {"nib"}}
	for name, argv := range cases {
		t.Run(name, func(t *testing.T) {
			emptyRootEnv(t)

			var code int
			_, errOut := captureProcessOutput(t, func() {
				code = Main(argv)
			})
			if code != 1 {
				t.Fatalf("Main(%v) exit = %d, want 1 (the setup abort)", argv, code)
			}
			if !strings.Contains(errOut, "no model configured") {
				t.Fatalf("stderr = %q, want the no-model abort", errOut)
			}
		})
	}
}

// Main returns the exit code rather than exiting: nib's main.go is the only
// thing allowed to call os.Exit.
func TestMainReturnsTheExitCode(t *testing.T) {
	emptyRootEnv(t)

	var code int
	_, errOut := captureProcessOutput(t, func() {
		code = Main([]string{"nib", "--init", "tcsh"})
	})
	if code != 1 {
		t.Fatalf("Main exit = %d, want 1", code)
	}
	if !strings.Contains(errOut, "tcsh") {
		t.Fatalf("stderr = %q, want it to name the bad shell", errOut)
	}
}

func TestRunReturnsNilOnSuccess(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), Options{Args: []string{"--version"}, Stdout: &out}); err != nil {
		t.Fatalf("Run(--version) = %v, want nil", err)
	}
	if !strings.Contains(out.String(), "nib") {
		t.Fatalf("stdout = %q, want the version line", out.String())
	}
}

// The exit code has to survive the trip out as a typed error, or an embedder
// cannot tell an unparseable flag (2) from everything else (1).
func TestRunWrapsTheExitCodeInAnExitError(t *testing.T) {
	cases := []struct {
		name string
		args []string
		code int
	}{
		{"unparseable flag", []string{"--bogus"}, 2},
		{"unknown init shell", []string{"--init", "tcsh"}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := Run(context.Background(), Options{Args: tc.args, Stdout: &out, Stderr: &out})
			if err == nil {
				t.Fatalf("Run(%v) = nil, want an error", tc.args)
			}
			var exit ExitError
			if !errors.As(err, &exit) {
				t.Fatalf("Run(%v) = %v (%T), want an ExitError", tc.args, err, err)
			}
			if exit.Code != tc.code {
				t.Fatalf("ExitError.Code = %d, want %d", exit.Code, tc.code)
			}
		})
	}
}

func TestExitErrorMessage(t *testing.T) {
	if got, want := (ExitError{Code: 3}).Error(), "exit status 3"; got != want {
		t.Fatalf("ExitError.Error() = %q, want %q", got, want)
	}
}

// Run takes its cancellation from the caller's context, which is the whole
// reason an embedder uses it instead of Main.
func TestRunHonorsTheCallerContext(t *testing.T) {
	var errOut bytes.Buffer
	o := skipSetupOptions(t, io.Discard, &errOut)
	o.SkipSetup = true

	err := Run(cancelledContext(t), o)
	if err == nil {
		t.Fatal("Run with a cancelled context = nil, want an error")
	}
	var exit ExitError
	if !errors.As(err, &exit) || exit.Code != 1 {
		t.Fatalf("Run = %v, want ExitError{Code: 1}", err)
	}
	if !strings.Contains(errOut.String(), "context canceled") {
		t.Fatalf("stderr = %q, want the cancellation to have reached the run", errOut.String())
	}
}
