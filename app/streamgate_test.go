package app

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"golang.org/x/term"
)

func TestDecideStreamRefusal(t *testing.T) {
	var (
		absent   = injectedStream{}
		buffer   = injectedStream{provided: true}
		terminal = injectedStream{provided: true, terminal: true}
	)
	cases := []struct {
		name         string
		mode         runMode
		stdin, stdou injectedStream
		want         string
	}{
		{"cli honors a buffer", modeCLI, buffer, buffer, ""},
		{"cli honors a terminal", modeCLI, terminal, terminal, ""},
		{"tui with nothing injected", modeTUI, absent, absent, ""},
		{"tui with injected terminals", modeTUI, terminal, terminal, ""},
		{"tui with a buffer for stdout", modeTUI, absent, buffer, "stdout"},
		{"tui with a buffer for stdin", modeTUI, buffer, absent, "stdin"},
		{"tui with a buffer for stdout only, terminal stdin", modeTUI, terminal, buffer, "stdout"},
		{"inline with a buffer for stdout", modeInline, absent, buffer, "stdout"},
		{"inline with injected terminals", modeInline, terminal, terminal, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decideStreamRefusal(c.mode, c.stdin, c.stdou); got != c.want {
				t.Fatalf("decideStreamRefusal = %q, want %q", got, c.want)
			}
		})
	}
}

func TestInjectedStreamProbes(t *testing.T) {
	if got := injectedReader(nil); got.provided {
		t.Fatalf("a nil reader must not count as injected: %+v", got)
	}
	if got := injectedWriter(nil); got.provided {
		t.Fatalf("a nil writer must not count as injected: %+v", got)
	}
	if got := injectedReader(strings.NewReader("")); !got.provided || got.terminal {
		t.Fatalf("a strings.Reader = %+v, want provided and not a terminal", got)
	}
	if got := injectedWriter(&bytes.Buffer{}); !got.provided || got.terminal {
		t.Fatalf("a bytes.Buffer = %+v, want provided and not a terminal", got)
	}
	// An *os.File is only a terminal when it really is one: under `go test`
	// stdout is a pipe, which is exactly the shape that must be refused.
	if got := injectedWriter(os.Stdout); !got.provided {
		t.Fatalf("os.Stdout = %+v, want provided", got)
	}
}

// The positive half of the probe, when the environment can supply a real
// terminal. Skipped rather than faked where it cannot: the decision function
// covers the terminal arm unconditionally in TestDecideStreamRefusal.
func TestInjectedWriterRecognizesARealTerminal(t *testing.T) {
	f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		t.Skip("no controlling terminal available")
	}
	defer f.Close()
	if !term.IsTerminal(int(f.Fd())) {
		t.Skip("/dev/tty is not a terminal here")
	}
	if got := injectedWriter(f); !got.provided || !got.terminal {
		t.Fatalf("injectedWriter(/dev/tty) = %+v, want provided and terminal", got)
	}
}

// End to end: the default mode is the TUI, so an embedder that injects a buffer
// and forgets --cli must be told, not silently rendered to /dev/tty with its
// buffer left empty. No TUI is started: the refusal happens before it.
func TestTUIRefusesAnInjectedNonTerminalStream(t *testing.T) {
	var out, errOut bytes.Buffer
	o := skipSetupOptions(t, &out, &errOut)
	o.Args = nil // default mode: the fullscreen TUI
	o.Defaults.Model = "seeded-model"

	if code := runCtx(cancelledContext(t), o); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	msg := errOut.String()
	if !strings.Contains(msg, "--cli") {
		t.Fatalf("the refusal must name --cli, got %q", msg)
	}
	if !strings.Contains(msg, "stdin") {
		t.Fatalf("the refusal must name the offending stream, got %q", msg)
	}
	if out.Len() != 0 {
		t.Fatalf("nothing should have been written to the injected stdout: %q", out.String())
	}
}

// The control: with --cli the very same injected streams are honored, so the
// gate cannot be "reject every embedder".
func TestCLIAcceptsAnInjectedNonTerminalStream(t *testing.T) {
	var out, errOut bytes.Buffer
	o := skipSetupOptions(t, &out, &errOut)
	o.Defaults.Model = "seeded-model"

	if code := runCtx(cancelledContext(t), o); code != 1 {
		t.Fatalf("exit code = %d, want 1 (the cancelled context, not the stream gate)", code)
	}
	if strings.Contains(errOut.String(), "--cli") {
		t.Fatalf("CLI mode must not refuse injected streams, got %q", errOut.String())
	}
	if !strings.Contains(errOut.String(), "context canceled") {
		t.Fatalf("the run should have reached the CLI, got %q", errOut.String())
	}
}
