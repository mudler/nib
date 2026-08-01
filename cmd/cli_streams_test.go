package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/types"
)

// runCLIScript drives RunCLI with a scripted stdin and captures both output
// streams. The script must end with "exit" so the loop returns cleanly instead
// of falling out on EOF. No turn is ever started, so nothing reaches an LLM.
func runCLIScript(t *testing.T, cfg types.Config, script string) (string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := RunCLI(context.Background(), cfg, Streams{
		In:  strings.NewReader(script),
		Out: &out,
		Err: &errOut,
	}, nil)
	if err != nil {
		t.Fatalf("RunCLI: %v", err)
	}
	return out.String(), errOut.String()
}

// The banner and the prompt must land on the injected stdout, and the loop must
// read the injected stdin. Without both, an embedder that hands nib its own
// streams is silently talking to the process streams instead.
func TestRunCLIUsesTheInjectedStreams(t *testing.T) {
	out, errOut := runCLIScript(t, types.Config{}, "help\nexit\n")

	if !strings.Contains(out, theme.CLIWelcome) {
		t.Fatalf("banner did not reach the injected stdout: %q", out)
	}
	// Once at startup, once for the scripted "help" line: the second occurrence
	// is the proof that the loop read from the injected reader.
	if n := strings.Count(out, "commands:"); n != 2 {
		t.Fatalf("want the help line twice (startup + scripted 'help'), got %d in %q", n, out)
	}
	if errOut != "" {
		t.Fatalf("nothing should have reached stderr, got %q", errOut)
	}
}

// Errors go to the injected stderr, not to the process stderr and not mixed
// into stdout.
func TestRunCLISendsErrorsToTheInjectedStderr(t *testing.T) {
	out, errOut := runCLIScript(t, types.Config{}, "/definitely-not-a-command\nexit\n")

	if !strings.Contains(errOut, "definitely-not-a-command") {
		t.Fatalf("resolve error did not reach the injected stderr: %q", errOut)
	}
	if strings.Contains(out, "definitely-not-a-command") {
		t.Fatalf("the error leaked into stdout: %q", out)
	}
}
