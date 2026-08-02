package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// inTmux returns true if running inside tmux
func IsInTmux() bool {
	return os.Getenv("TMUX") != "" && os.Getenv("TMUX_PANE") != ""
}

// runTmuxSplit runs nib in a tmux split pane (like fzf-tmux -d).
//
// `tmux split-window` returns as soon as the pane is spawned, not when its
// command exits, so we can't capture the inner nib's selected command from its
// stdout directly. Instead the inner nib writes its command to a temp file and
// signals a tmux channel on exit; we block on that channel, then relay the file
// to our own stdout — which is what the Ctrl+Space shell widget captures.
//
// programName is how the user reaches this program, and it is load-bearing
// rather than cosmetic here: the pane has to re-enter nib, and for an embedder
// that means the host binary PLUS its subcommand. Empty means "nib". See
// tmuxSelfInvocation.
//
// The temp file prefix and the tmux channel name stay "nib"-flavored. They are
// internal identifiers, never shown and never typed: the prefix goes to
// os.CreateTemp and the channel is that file's basename, so both are unique per
// invocation and neither is affected by a program name of any shape.
//
// traceDir is the already-resolved trace directory ("" for no tracing), and it
// has to be passed rather than inherited: `tmux split-window` hands the new
// pane the tmux SERVER's environment, not this process's, so NIB_TRACE_DIR
// exported in the user's shell does not survive the pane boundary. It is
// forwarded as an explicit --trace-dir flag instead. The caller has already
// preflighted it, so nothing here re-checks that the directory is writable.
func RunTmuxSplit(programName, height, traceDir string) error {
	// Get current working directory
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}

	// The binary the pane should re-enter. An empty string means the lookup
	// failed and tmuxInnerCommand should fall back to the program name.
	executable, err := os.Executable()
	if err != nil {
		executable = ""
	}

	// Temp file the inner nib's stdout (the selected command) is captured into,
	// plus a tmux wait-for channel keyed off its unique name.
	tmp, err := os.CreateTemp("", "nib-yank-*")
	if err != nil {
		return err
	}
	outPath := tmp.Name()
	tmp.Close()
	defer os.Remove(outPath)
	channel := "nib-" + filepath.Base(outPath)

	inner := tmuxInnerCommand(programName, executable, height, traceDir, outPath, channel)

	// -v vertical split (pane below), -l height, -c working dir. Focus moves to
	// the new pane so the user interacts with nib.
	split := exec.Command("tmux", "split-window", "-v", "-l", height, "-c", dir, "sh", "-c", inner)
	split.Stdin = os.Stdin
	split.Stderr = os.Stderr
	if err := split.Run(); err != nil {
		return err
	}

	// Block until the inner nib exits and signals the channel.
	_ = exec.Command("tmux", "wait-for", channel).Run()

	// Relay the captured command to our stdout for the shell widget to insert.
	data, err := os.ReadFile(outPath)
	if err != nil {
		return nil // nothing captured (e.g. user cancelled) — not an error
	}
	fmt.Print(string(data))
	return nil
}

// tmuxInnerCommand builds the shell line the split pane runs: re-enter this
// program with --no-tmux (so it renders instead of splitting again), redirect
// its stdout to the capture file, then signal the tmux channel the outer
// process is blocked on.
//
// Pure, and separated from RunTmuxSplit for exactly that reason: RunTmuxSplit
// shells out to tmux, so the only way to assert what the pane is actually told
// to run is to build the string somewhere a test can call.
//
// An empty executable means os.Executable() failed and the program name is the
// only thing left to go on.
func tmuxInnerCommand(programName, executable, height, traceDir, outPath, channel string) string {
	// The trace dir travels as a flag rather than through the environment
	// because `tmux split-window` gives the pane the tmux server's environment,
	// not this process's: NIB_TRACE_DIR set in the user's shell never reaches
	// it. Empty means no tracing, and must add nothing — the standalone command
	// line is pinned byte for byte by TestTmuxInnerCommandStandaloneIsUnchanged.
	//
	// Quoted as ONE word, unlike the invocation above: this is a single path
	// argument, so a dir with a space in it has to stay a single argument.
	trace := ""
	if traceDir != "" {
		trace = " --trace-dir " + shellQuote(traceDir)
	}
	return fmt.Sprintf("%s --height %s --no-tmux%s > %s; tmux wait-for -S %s",
		tmuxSelfInvocation(programName, executable), height, trace, shellQuote(outPath), shellQuote(channel))
}

// tmuxSelfInvocation renders how the pane re-enters THIS program.
//
// The executable path alone is not the invocation. An embedder reaches nib as a
// subcommand, so os.Executable() hands back the host binary with the subcommand
// missing: for `local-ai chat` the pane would run `local-ai --height 50%
// --no-tmux`, and since LocalAI's `run` is its default command that does not
// even fail cleanly, it tries to boot a server in the split. So the program
// name supplies the words and os.Executable() supplies only the FIRST of them,
// replacing the binary's name with the absolute path nib is actually running
// from. Standalone that leaves exactly the path, which is what it always was.
//
// The result is interpolated into an `sh -c` string, so each word is quoted
// separately with the same helper initScriptCommand uses, and for the same
// reason: quoting the invocation as a unit would make sh look for a single
// command literally called "local-ai chat". shellQuoteWord leaves a word alone
// when every byte in it is safe, which is what keeps an ordinary executable
// path bare and the standalone line byte-identical.
//
// An empty executable means the lookup failed. Standalone that has always meant
// the bare name and a PATH lookup; embedded, the program name is the only thing
// left that can still name the right binary, so it is the fallback rather than
// a hardcoded "nib" that is not installed.
func tmuxSelfInvocation(programName, executable string) string {
	words := strings.Fields(programName)
	if len(words) == 0 {
		words = []string{defaultProgramName}
	}
	if executable != "" {
		words[0] = executable
	}
	quoted := make([]string, 0, len(words))
	for _, w := range words {
		quoted = append(quoted, shellQuoteWord(w))
	}
	return strings.Join(quoted, " ")
}

// shellQuote single-quotes s for safe interpolation into the `sh -c` string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
