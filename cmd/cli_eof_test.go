package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

// runCLIUntilInputEnds drives RunCLI with a scripted stdin that, unlike
// runCLIScript's, is not terminated by "exit": the reader simply runs out,
// which is what a pipe does.
func runCLIUntilInputEnds(t *testing.T, script string) (error, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := RunCLI(context.Background(), types.Config{}, Streams{
		In:  strings.NewReader(script),
		Out: &out,
		Err: &errOut,
	}, nil)
	return err, out.String()
}

// The documented one-shot idiom is `echo "question" | nib --cli`, and a script
// using it saw the answer and then a false failure: EOF came back from the read
// as an error and out of RunCLI as one, so the process exited 1 with
// "Error: EOF". Running out of input is the end of the input, not a failure.
func TestRunCLIEndsCleanlyWhenStdinRunsOut(t *testing.T) {
	err, _ := runCLIUntilInputEnds(t, "help\n")
	if err != nil {
		t.Fatalf("RunCLI on exhausted stdin = %v, want nil", err)
	}
}

// `printf 'question' | nib --cli` puts no newline after the last line, so the
// read returns that text alongside the EOF. Treating the EOF as the end of
// input must not turn into dropping the line that came with it.
func TestRunCLIHandlesALastLineWithoutANewline(t *testing.T) {
	err, out := runCLIUntilInputEnds(t, "help")
	if err != nil {
		t.Fatalf("RunCLI on a newline-less last line = %v, want nil", err)
	}
	// Once at startup, once for the scripted "help": the second occurrence is
	// the proof the trailing line was handled rather than discarded.
	if n := strings.Count(out, "commands:"); n != 2 {
		t.Fatalf("want the help line twice (startup + the trailing 'help'), got %d in %q", n, out)
	}
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

// EOF is the only read outcome that ends the loop quietly. A stdin that is
// genuinely broken still has to be reported, or a redirect from a file on a
// dying disk looks exactly like a session the user finished.
func TestRunCLIReportsAReadErrorThatIsNotEOF(t *testing.T) {
	boom := errors.New("stdin is on fire")
	err := RunCLI(context.Background(), types.Config{}, Streams{
		In:  failingReader{err: boom},
		Out: &bytes.Buffer{},
		Err: &bytes.Buffer{},
	}, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("RunCLI on a broken stdin = %v, want %v", err, boom)
	}
}

// Ctrl+C cancels the context rather than closing stdin, and it has to stay
// distinguishable from the end of input: an interrupted session is a failure,
// an exhausted one is not.
func TestRunCLIStillReportsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := RunCLI(ctx, types.Config{}, Streams{
		In:  strings.NewReader("help\n"),
		Out: &bytes.Buffer{},
		Err: &bytes.Buffer{},
	}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunCLI under a cancelled context = %v, want context.Canceled", err)
	}
}
