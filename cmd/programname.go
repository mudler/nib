package cmd

import "github.com/mudler/nib/types"

// runnableName renders the program name for a line the user is told to TYPE:
// the management subcommands' usage strings, and the hints that end an install
// ("Enable later: ..."). An empty name means "nib", so standalone nib's output
// is byte-identical to what it was before an embedder could rename it.
//
// It is a thin alias for types.ProgramNameOr rather than a second copy of the
// rule. The same rendering has to hold for the skill installer's suggestions
// and for the sentence in the system prompt, both of which live in packages cmd
// cannot reach, so the rule belongs in the one package all three share. This
// name exists only because it reads better at the call sites here.
//
// It is the printed sibling of initScriptCommand, and it deliberately does
// less. That one interpolates the name into a shell script, so it quotes each
// word; this one only ever reaches a terminal, so quoting would be noise in the
// common case and would misrepresent what the user should actually type.
func runnableName(programName string) string { return types.ProgramNameOr(programName) }
