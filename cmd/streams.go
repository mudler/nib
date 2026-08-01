package cmd

import (
	"io"
	"os"

	"golang.org/x/term"
)

// Streams are the I/O endpoints a front end reads from and writes to. A nil
// field means the matching process stream, so the zero value reproduces
// standalone nib exactly and an embedder overrides only what it cares about.
//
// The fields stay nil-able rather than being defaulted at construction: RunTUI
// has to tell "the embedder handed me a stream" apart from "nobody said
// anything", because the two want different terminals (see RunTUI).
type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

func (s Streams) stdin() io.Reader {
	if s.In == nil {
		return os.Stdin
	}
	return s.In
}

func (s Streams) stdout() io.Writer {
	if s.Out == nil {
		return os.Stdout
	}
	return s.Out
}

func (s Streams) stderr() io.Writer {
	if s.Err == nil {
		return os.Stderr
	}
	return s.Err
}

// isTerminal reports whether w is an interactive terminal. A writer that is not
// an *os.File (a buffer, a pipe an embedder handed us) never is.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
