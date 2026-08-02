package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/chat"
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/tui"
	"github.com/mudler/nib/types"
)

// RunTUI runs the Bubble Tea TUI.
//
// Only streams.stdout() is honored, for the shell-capture line at the end. The
// interactive rendering deliberately stays on /dev/tty: the whole reason this
// function opens it is that stdout may be a pipe while the terminal is still
// there, and an embedder's Stdout is exactly as likely to be a pipe as the
// shell widget's. Rendering into a non-terminal writer would produce a frozen
// TUI, which is worse than ignoring the injection. An embedder that wants nib's
// output on its own streams should use CLI mode, which honors all three.
//
// Cancelling ctx unwinds the program and returns ctx.Err(). Signals are the
// caller's: app.run installs the handler for standalone nib, and an embedder
// installs its own, because the program itself no longer listens for any. See
// tuiProgramOptions for why that has to be exactly one owner.
func RunTUI(ctx context.Context, cfg types.Config, height int, streams Streams, shellJobs *wizmcp.ShellJobs, transports ...mcp.Transport) error {

	model := tui.NewModel(ctx, cfg, height, shellJobs, transports...)

	// Open /dev/tty directly for TUI - this is crucial when stdout is being captured
	// (e.g., when run from a shell widget like `output=$(wiz --height 40%)`)
	ttyIn, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("failed to open /dev/tty for reading: %w", err)
	}
	defer ttyIn.Close()

	ttyOut, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("failed to open /dev/tty for writing: %w", err)
	}
	defer ttyOut.Close()

	// Calculate actual height for inline mode
	// Like fzf, we render inline at the bottom of the terminal
	termHeight := getTerminalHeight()
	actualHeight := height
	if height < 0 {
		// Negative means percentage
		actualHeight = (termHeight * (-height)) / 100
	}
	if actualHeight > termHeight {
		actualHeight = termHeight
	}
	if actualHeight < 5 {
		actualHeight = 5
	}

	// Make space for the TUI by printing newlines (like fzf does)
	// This pushes the existing content up
	for i := 0; i < actualHeight; i++ {
		fmt.Fprint(ttyOut, "\n")
	}
	// Move cursor up to the start of our space
	fmt.Fprintf(ttyOut, "\x1b[%dA", actualHeight)
	// Move to beginning of line
	fmt.Fprint(ttyOut, "\x1b[G")

	p := tea.NewProgram(model, tuiProgramOptions(ctx, ttyIn, ttyOut)...)

	finalModel, runErr := p.Run()

	// Clear the space we used (move to start and clear to end of screen)
	fmt.Fprint(ttyOut, "\x1b[G") // Move to beginning of line
	fmt.Fprint(ttyOut, "\x1b[J") // Clear from cursor to end of screen

	// ttyOut, never stdout: stdout is the shell-capture stream (see below), so a
	// summary there would be inserted into the user's command line. The TUI runs
	// without an alt screen, so this lands in scrollback normally. Printed before
	// the exit check because the tokens were spent either way.
	if m, ok := finalModel.(tui.Model); ok {
		if s := chat.FormatSessionSummary(m.SessionUsage()); s != "" {
			fmt.Fprintln(ttyOut, s)
		}
	}

	// Every non-nil runErr maps to a non-nil result, so past this point the
	// program quit rather than being killed and the model has a final state.
	if err := decideTUIExit(runErr, ctx.Err()); err != nil {
		return err
	}

	// Output any command to shell if needed (this goes to stdout for shell
	// capture, which is the one stream an embedder can usefully replace here)
	if m, ok := finalModel.(tui.Model); ok {
		if output := m.Output(); output != "" {
			fmt.Fprint(streams.stdout(), output)
		}
	}

	return nil
}

// tuiProgramOptions are the bubbletea options the TUI runs under.
//
// tea.WithContext is the point of the pair. Without it bubbletea knows nothing
// about the caller's context, and cancelling it does not stop the program: the
// only reason SIGINT and SIGTERM appeared to work is that bubbletea installs
// its own handler for exactly those two. Anything else that cancels, an
// embedder's SIGHUP handling or its own shutdown path, left a live TUI running
// on a context whose every send already failed, with Run never returning.
//
// tea.WithoutSignalHandler is not optional once the context is wired, and the
// reason is a deadlock rather than a preference. bubbletea's signal goroutine
// sends InterruptMsg on an unbuffered channel that only the event loop reads,
// and the event loop also exits on the context being cancelled. nib's own
// handler cancels on the very same SIGINT, so the two race: when the
// cancellation wins, the signal goroutine is left blocked on a send nobody will
// ever receive, and Run's shutdown waits for that goroutine forever. Measured
// against v1.3.10, `kill -INT` hung roughly three runs in five with both
// mechanisms live. One owner for the signal is the fix, and it is the outer
// one: standalone nib installs its handler in app.run, and an embedder owns its
// own, which is what app.Run already documents.
//
// It is a function so a test can build the same program over a pipe and prove
// cancellation unwinds it, which is not something RunTUI can be asked to
// demonstrate: RunTUI needs a controlling terminal.
func tuiProgramOptions(ctx context.Context, in io.Reader, out io.Writer) []tea.ProgramOption {
	// No alt screen: render inline like fzf, on /dev/tty rather than on stdout.
	return []tea.ProgramOption{
		tea.WithContext(ctx),
		tea.WithoutSignalHandler(),
		tea.WithInput(in),
		tea.WithOutput(out),
	}
}

// decideTUIExit maps what tea.Program.Run returned onto what RunTUI returns.
// ctxErr is ctx.Err() read after Run came back.
//
// Cancelling the context unwinds the program, and bubbletea reports every
// unwind as a kill, wrapping whatever it thinks the cause was. The caller
// already knows the cause, so a killed program on a cancelled context reports
// the context's own error, the way CLI mode does. That keeps a signal on a
// standalone nib exiting non-zero, and gives an embedder that cancelled the
// same error from the TUI that it gets from the CLI.
func decideTUIExit(runErr, ctxErr error) error {
	switch {
	case runErr == nil:
		return nil
	case errors.Is(runErr, tea.ErrProgramKilled) && ctxErr != nil:
		return ctxErr
	default:
		return runErr
	}
}

// getTerminalHeight returns the terminal height
func getTerminalHeight() int {
	// Try to get terminal size
	cmd := exec.Command("tput", "lines")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err == nil {
		if h, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && h > 0 {
			return h
		}
	}

	// Fallback: try stty
	cmd = exec.Command("stty", "size")
	cmd.Stdin, _ = os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	out, err = cmd.Output()
	if err == nil {
		parts := strings.Fields(string(out))
		if len(parts) >= 1 {
			if h, err := strconv.Atoi(parts[0]); err == nil && h > 0 {
				return h
			}
		}
	}

	// Default
	return 24
}
