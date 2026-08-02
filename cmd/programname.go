package cmd

import "strings"

// runnableName renders the program name for a line the user is told to TYPE:
// the management subcommands' usage strings, and the hints that end an install
// ("Enable later: ..."). An empty name means "nib", so standalone nib's output
// is byte-identical to what it was before an embedder could rename it.
//
// This is the printed sibling of initScriptCommand, and it deliberately does
// less. That one interpolates the name into a shell script, so it quotes each
// word; this one only ever reaches a terminal, so quoting would be noise in the
// common case and would misrepresent what the user should actually type.
//
// What it does share is refusing to paste an embedder's string through
// untouched. The words are re-joined with single spaces, which flattens a name
// carrying a newline: printed unflattened it could forge output lines of its
// own, and every message this feeds is one the reader treats as nib speaking.
func runnableName(programName string) string {
	words := strings.Fields(programName)
	if len(words) == 0 {
		return defaultProgramName
	}
	return strings.Join(words, " ")
}
