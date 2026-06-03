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
func RunTmuxSplit(height string) error {
	// Get current working directory
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}

	// Get the nib executable path
	executable, err := os.Executable()
	if err != nil {
		executable = "nib"
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

	// Inside the pane: run nib (with --no-tmux to avoid recursion), redirect its
	// stdout to the temp file, then signal the channel so we can stop waiting.
	inner := fmt.Sprintf("%s --height %s --no-tmux > %s; tmux wait-for -S %s",
		executable, height, shellQuote(outPath), shellQuote(channel))

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

// shellQuote single-quotes s for safe interpolation into the `sh -c` string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
