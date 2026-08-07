package cmd

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
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

// syncBuf is a bytes.Buffer safe to read while the spinner goroutine writes.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// waitForFrame blocks until the spinner draws a frame carrying msg at or after
// byte offset from, and returns everything written from there. The offset is
// what makes "the spinner came back" a real assertion: without it a frame drawn
// before the notice would satisfy the wait.
func waitForFrame(t *testing.T, buf *syncBuf, from int, msg string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s := buf.String(); len(s) >= from && strings.Contains(s[from:], msg) {
			return s[from:]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("spinner never drew a frame for %q after byte %d; got %q", msg, from, buf.String())
	return ""
}

// A mid-run notice must never be appended to the spinner's half-drawn line —
// "⠋ working…pruned 2 tool results" is what happens when it is. The spinner
// line is cleared first, and put back afterwards showing the SAME verb: the
// spinner was mid-"working" and a notice arriving is no reason to tell the user
// it is now thinking.
func TestWriteNoticeClearsSpinnerLineAndKeepsVerb(t *testing.T) {
	buf := &syncBuf{}
	s := newSpinner(buf)
	s.tty = true // a test writer is never a real terminal; exercise the TTY path

	s.start("working…")
	waitForFrame(t, buf, 0, "working…")

	writeNotice(buf, s, "NOTICE")

	out := buf.String()
	// The notice starts a clean line: the clear sequence immediately precedes it.
	if !strings.Contains(out, "\r\033[KNOTICE\n") {
		t.Fatalf("notice was not written on a cleared line: %q", out)
	}

	// And the spinner comes back, with its verb intact.
	after := waitForFrame(t, buf, len(out), "working…")
	if strings.Contains(after, "thinking") {
		t.Fatalf("resuming the spinner stomped its verb: %q", after)
	}
	s.stop()
}

// The compaction notice can arrive after OnResponse has already stopped the
// spinner. Restarting one that was not running would leave it spinning at the
// prompt forever, so a notice printed while nothing is animating just prints.
func TestWriteNoticeDoesNotStartAnIdleSpinner(t *testing.T) {
	buf := &syncBuf{}
	s := newSpinner(buf)
	s.tty = true

	writeNotice(buf, s, "NOTICE")
	time.Sleep(200 * time.Millisecond) // several frame intervals

	if got := buf.String(); got != "NOTICE\n" {
		t.Fatalf("idle spinner was resurrected by a notice: %q", got)
	}
}

// In non-TTY mode nothing is redrawn in place, so there is no line to clear —
// and stopping would make the next start() reprint the status, padding a CI log
// with a status line after every notice.
func TestWriteNoticeNonTTYLeavesTheStatusLogAlone(t *testing.T) {
	buf := &syncBuf{}
	s := newSpinner(buf)
	if s.tty {
		t.Fatal("a plain writer must never be taken for a terminal")
	}

	s.start("working…")
	writeNotice(buf, s, "NOTICE")
	s.update("working…")

	out := buf.String()
	if strings.Contains(out, "\r") {
		t.Fatalf("non-TTY output must not contain carriage returns: %q", out)
	}
	if n := strings.Count(out, "working…"); n != 1 {
		t.Fatalf("expected the status line printed once, got %d in %q", n, out)
	}
	if !strings.Contains(out, "NOTICE\n") {
		t.Fatalf("notice missing from non-TTY output: %q", out)
	}
}
