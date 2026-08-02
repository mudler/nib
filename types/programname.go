package types

import "strings"

// DefaultProgramName is what nib calls itself when no embedder has renamed it.
const DefaultProgramName = "nib"

// ProgramNameOr renders a program name for a string a human will read as a
// command to type: a usage line, an "enable it later" hint, or the sentence in
// the system prompt that the MODEL will relay as advice. Empty means "nib", so
// standalone nib is unchanged wherever this is used.
//
// It lives here, in the lowest package the callers share, so the rule has ONE
// definition. cmd, skill and the prompt renderer all go through it; a second
// copy would be a second thing to keep in step.
//
// The words are re-joined with single spaces. That is not cosmetic: the name
// comes from an embedder, and every string this feeds is either read as nib
// speaking or handed to a model as instructions. A name carrying a newline
// would otherwise be able to forge a line of either.
func ProgramNameOr(programName string) string {
	words := strings.Fields(programName)
	if len(words) == 0 {
		return DefaultProgramName
	}
	return strings.Join(words, " ")
}
