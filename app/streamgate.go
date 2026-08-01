package app

import (
	"io"
	"os"

	"golang.org/x/term"
)

// injectedStream describes one stream an embedder handed to Options: whether it
// was provided at all, and whether it is an interactive terminal.
type injectedStream struct {
	provided bool
	terminal bool
}

// decideStreamRefusal names an injected stream the selected mode cannot use, or
// returns "" when the run may proceed.
//
// Only CLI mode reads and writes the injected streams. Every other mode renders
// on /dev/tty (see RunTUI), so a buffer, pipe or file handed in for stdin or
// stdout would be silently ignored: the embedder's output would go to a
// terminal it may not even own, and its input would never be read. Refusing,
// with --cli named, is the honest answer.
//
// Non-nil-ness alone is deliberately NOT the test. An embedder that passes the
// process streams while sitting on a real terminal is the common case, and the
// TUI serves it perfectly; the streams it hands over are the same ones nib
// would have used. What the TUI genuinely cannot do is render into something
// that is not a terminal.
func decideStreamRefusal(m runMode, stdin, stdout injectedStream) string {
	if m == modeCLI {
		return ""
	}
	if stdin.provided && !stdin.terminal {
		return "stdin"
	}
	if stdout.provided && !stdout.terminal {
		return "stdout"
	}
	return ""
}

// injectedReader / injectedWriter probe what an embedder actually handed over.
// A nil stream is not injected at all: nib falls back to the process stream and
// standalone behavior is untouched.
func injectedReader(r io.Reader) injectedStream {
	f, ok := r.(*os.File)
	return injectedStream{provided: r != nil, terminal: ok && term.IsTerminal(int(f.Fd()))}
}

func injectedWriter(w io.Writer) injectedStream {
	f, ok := w.(*os.File)
	return injectedStream{provided: w != nil, terminal: ok && term.IsTerminal(int(f.Fd()))}
}
