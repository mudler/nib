//go:build unix

package cmd

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// The double-quit deadlock, guarded.
//
// bubbletea's own signal goroutine sends InterruptMsg on an unbuffered channel
// that only its event loop reads, and that loop also exits when the context is
// cancelled. nib's handler cancels on the very same SIGINT, so with both
// mechanisms live the two race: when the cancellation wins, bubbletea's signal
// goroutine is left blocked on a send nobody will ever receive, and Run's
// shutdown waits for that goroutine forever. Measured against v1.3.10, wiring
// the context in while leaving bubbletea's handler on hung `kill -INT` in a
// large fraction of runs, which is why RunTUI owns the signal exactly once.
//
// This reproduces the standalone shape: a handler like app.run's that cancels
// the context, a program built the way RunTUI builds it, and a real SIGINT
// raised on this process only, and only while the handler below is installed,
// so nothing outside this test can see it.
//
// The deadlock is a race, so one attempt catches it only sometimes. The loop is
// what turns a coin flip into a sentinel that usually fires; it cannot fail
// spuriously, because a program that stops unwinding is never correct.
func TestSigintDoesNotDeadlockTheTUIProgram(t *testing.T) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT)
	defer signal.Stop(sigs)

	for attempt := range 8 {
		ctx, cancel := context.WithCancel(context.Background())

		// One waiter per attempt, consuming that attempt's signal, doing
		// exactly what app.run's handler does with it.
		go func() {
			select {
			case <-sigs:
				cancel()
			case <-ctx.Done():
			}
		}()

		// An io.Pipe, not an os.Pipe. A file input takes bubbletea's epoll
		// cancelreader, whose Close races its own read loop on the kill path
		// in v1.3.10, and that upstream race is not what this test is about.
		in, inWriter := io.Pipe()

		p := tea.NewProgram(idleTUIModel{}, tuiProgramOptions(ctx, in, io.Discard)...)
		done := make(chan error, 1)
		go func() {
			_, runErr := p.Run()
			done <- runErr
		}()

		time.Sleep(100 * time.Millisecond) // let the program come up
		if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
			t.Fatalf("attempt %d: raising SIGINT: %v", attempt, err)
		}

		select {
		case runErr := <-done:
			// Non-zero for the caller, as a signalled run has always been.
			if got := decideTUIExit(runErr, ctx.Err()); got == nil {
				t.Fatalf("attempt %d: Run err = %v, but RunTUI would report success", attempt, runErr)
			}
		case <-time.After(5 * time.Second):
			p.Kill()
			t.Fatalf("attempt %d: SIGINT deadlocked the program, Run did not return within 5s", attempt)
		}

		inWriter.Close()
		cancel()
	}
}
