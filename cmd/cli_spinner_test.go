package cmd

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout redirects os.Stdout for the duration of fn and returns what was
// written to it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		br := bufio.NewReader(r)
		_, _ = io.Copy(&sb, br)
		done <- sb.String()
	}()

	fn()

	_ = w.Close()
	os.Stdout = orig
	return <-done
}

// In non-TTY mode (pipes, CI logs), the spinner must not animate: it emits each
// distinct status once as its own line, collapsing a steady "thinking" state
// into a single entry instead of hundreds of carriage-return frames.
func TestSpinnerNonTTYDeduplicates(t *testing.T) {
	var buf bytes.Buffer
	s := newSpinner(&buf)
	if s.tty {
		t.Fatal("a plain buffer must never be taken for a terminal")
	}

	s.start("thinking")
	s.update("thinking") // duplicate, must not reprint
	s.update("thinking")
	s.stop()

	out := buf.String()
	if strings.Contains(out, "\r") {
		t.Fatalf("non-TTY output must not contain carriage returns, got %q", out)
	}
	if n := strings.Count(out, "thinking"); n != 1 {
		t.Fatalf("expected a single deduplicated 'thinking' line, got %d in %q", n, out)
	}
}

// After a stop()/start() cycle (e.g. across a tool-call boundary) the same
// status reprints, so progress stays visible in the log.
func TestSpinnerNonTTYReprintsAfterStop(t *testing.T) {
	var buf bytes.Buffer
	s := newSpinner(&buf)

	s.start("thinking")
	s.stop() // tool call interleaves here
	s.start("thinking")
	s.stop()

	if n := strings.Count(buf.String(), "thinking"); n != 2 {
		t.Fatalf("expected 'thinking' reprinted after stop, got %d in %q", n, buf.String())
	}
}
