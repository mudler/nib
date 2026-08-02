package cmd

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// idleTUIModel never quits on its own, so the only thing that can end a program
// running it is the wiring under test.
type idleTUIModel struct{}

func (idleTUIModel) Init() tea.Cmd                         { return nil }
func (m idleTUIModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (idleTUIModel) View() string                          { return "idle" }

// Cancelling the context must unwind the program. Before tea.WithContext,
// bubbletea knew nothing about the caller's context: SIGINT and SIGTERM
// appeared to work only because bubbletea installs its own handler for those
// two, and any other cancellation left the program running forever on a
// context whose every send already failed, with Run never returning.
//
// This drives a program over a pipe rather than through RunTUI, which needs a
// controlling terminal, but it is the same option set RunTUI runs under.
func TestTUIProgramUnwindsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in, inWriter := io.Pipe()
	defer inWriter.Close()

	p := tea.NewProgram(idleTUIModel{}, tuiProgramOptions(ctx, in, io.Discard)...)

	done := make(chan error, 1)
	go func() {
		_, err := p.Run()
		done <- err
	}()

	// Let the program come up, so the cancellation lands on a running program
	// rather than racing its startup.
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case runErr := <-done:
		if runErr == nil {
			t.Fatal("Run returned nil on a cancelled context, want the kill it reports")
		}
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("Run error = %v, want it to carry context.Canceled", runErr)
		}
		// The caller already knows why, so RunTUI reports the context's own
		// error rather than bubbletea's account of the unwind.
		if got := decideTUIExit(runErr, ctx.Err()); !errors.Is(got, context.Canceled) {
			t.Fatalf("decideTUIExit(%v) = %v, want context.Canceled", runErr, got)
		}
	case <-time.After(5 * time.Second):
		p.Kill()
		t.Fatal("cancelling the context did not unwind the program within 5s")
	}
}

// What each outcome maps to. The kill wrappings are what bubbletea actually
// returned in each case, measured against v1.3.10.
func TestDecideTUIExit(t *testing.T) {
	killed := func(cause error) error {
		return errors.Join(tea.ErrProgramKilled, cause)
	}
	boom := errors.New("render exploded")

	cases := []struct {
		name    string
		runErr  error
		ctxErr  error
		wantErr error
	}{
		// The everyday path: the user quit, or pressed the Ctrl+C the terminal
		// delivers as a keystroke rather than a signal. Exit 0, as always.
		{"quit normally", nil, nil, nil},
		{"quit normally after a cancel we did not act on", nil, context.Canceled, nil},
		// A signal, or an embedder shutting down: whoever cancelled hears its
		// own error back, the same one CLI mode returns.
		{"cancelled", killed(context.Canceled), context.Canceled, context.Canceled},
		{"cancelled by a deadline", killed(context.DeadlineExceeded), context.DeadlineExceeded, context.DeadlineExceeded},
		// A model that returns tea.Interrupt with nobody cancelling still
		// surfaces bubbletea's own error.
		{"interrupted", killed(tea.ErrInterrupted), nil, killed(tea.ErrInterrupted)},
		// A real failure is still a failure, cancelled context or not.
		{"failed", boom, nil, boom},
		{"failed while cancelling", boom, context.Canceled, boom},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decideTUIExit(c.runErr, c.ctxErr)
			switch {
			case c.wantErr == nil && got != nil:
				t.Fatalf("decideTUIExit(%v, %v) = %v, want nil", c.runErr, c.ctxErr, got)
			case c.wantErr != nil && got == nil:
				t.Fatalf("decideTUIExit(%v, %v) = nil, want %v", c.runErr, c.ctxErr, c.wantErr)
			case c.wantErr != nil && got.Error() != c.wantErr.Error():
				t.Fatalf("decideTUIExit(%v, %v) = %v, want %v", c.runErr, c.ctxErr, got, c.wantErr)
			}
		})
	}
}

// The invariant RunTUI leans on when it stops checking runErr after
// decideTUIExit: a program that did not come back clean never maps to a
// success, so anything past that check has a final model state to capture.
func TestDecideTUIExitNeverReportsAFailureAsSuccess(t *testing.T) {
	runErrs := []error{
		errors.New("render exploded"),
		tea.ErrProgramKilled,
		errors.Join(tea.ErrProgramKilled, context.Canceled),
		errors.Join(tea.ErrProgramKilled, tea.ErrInterrupted),
		errors.Join(tea.ErrProgramKilled, context.DeadlineExceeded),
	}
	for _, ctxErr := range []error{nil, context.Canceled, context.DeadlineExceeded} {
		for _, runErr := range runErrs {
			if got := decideTUIExit(runErr, ctxErr); got == nil {
				t.Fatalf("decideTUIExit(%v, %v) = nil: a program that was killed reported success", runErr, ctxErr)
			}
		}
	}
}
