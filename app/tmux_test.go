package app

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

// End to end for the tmux split, which is the mode the Ctrl+Space widget lands
// in whenever the user is inside tmux. The unit tests in cmd cover how the
// pane's command is BUILT; this one covers that Options.ProgramName is what
// gets handed to the builder, because a wiring slip there is invisible to both
// the compiler and the cmd tests.
//
// tmux is stubbed rather than required: a recording script on PATH captures the
// argv nib hands `tmux split-window`, which is the only place the pane's
// command exists before tmux would run it.
func TestTmuxSplitReEntersTheEmbeddedProgram(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on this host")
	}
	recorded := stubTmux(t)

	var errOut bytes.Buffer
	o := Options{
		ProgramName: "local-ai chat",
		BaseDir:     t.TempDir(),
		// Exactly what the Ctrl+Space widget runs: --height selects modeInline,
		// and being inside tmux turns that into a split.
		Args:        []string{"--height", "50%"},
		Defaults:    types.Config{Model: "m", BaseURL: "http://127.0.0.1:1/v1"},
		SkipSetup:   true,
		SkipBareEnv: true,
		// Stdin and Stdout stay nil: injecting non-terminal streams would be
		// refused before the tmux branch is ever reached.
		Stderr: &errOut,
	}
	runCtx(context.Background(), o)

	inner := innerCommandFrom(t, recorded)
	if !strings.Contains(inner, " chat --height ") {
		t.Fatalf("the pane does not re-enter `local-ai chat`, so it would start the host's default command instead: %q", inner)
	}
	if !strings.Contains(inner, "--no-tmux") {
		t.Fatalf("the pane command lost --no-tmux and would split again: %q", inner)
	}
}

// The standalone control: with no ProgramName there is no subcommand to add, so
// the pane command is the executable and the flags, exactly as before.
func TestTmuxSplitStandaloneAddsNoSubcommand(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on this host")
	}
	recorded := stubTmux(t)

	var errOut bytes.Buffer
	o := Options{
		Args:        []string{"--height", "50%"},
		BaseDir:     t.TempDir(),
		Defaults:    types.Config{Model: "m", BaseURL: "http://127.0.0.1:1/v1"},
		SkipSetup:   true,
		SkipBareEnv: true,
		Stderr:      &errOut,
	}
	runCtx(context.Background(), o)

	inner := innerCommandFrom(t, recorded)
	head, _, ok := strings.Cut(inner, " --height ")
	if !ok {
		t.Fatalf("unrecognized pane command: %q", inner)
	}
	if strings.Contains(head, " ") {
		t.Fatalf("standalone nib gained a subcommand it does not have: %q", head)
	}
}

// stubTmux puts a recording `tmux` at the front of PATH and tells nib it is
// inside tmux. It returns the path the recorded argv is written to.
func stubTmux(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	recorded := filepath.Join(dir, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> " + recorded + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	t.Setenv("TMUX_PANE", "%0")
	return recorded
}

// innerCommandFrom pulls the pane's command out of the recorded
// `tmux split-window -v -l H -c DIR sh -c CMD` argv. It anchors on the "sh"
// rather than on "-c", because split-window's own -c (the working directory)
// comes first and would match.
func innerCommandFrom(t *testing.T, recorded string) string {
	t.Helper()
	data, err := os.ReadFile(recorded)
	if err != nil {
		t.Fatalf("tmux was never invoked, so the split never happened: %v", err)
	}
	args := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	for i, a := range args {
		if a == "sh" && i+2 < len(args) && args[i+1] == "-c" {
			return args[i+2]
		}
	}
	t.Fatalf("no `sh -c` command in the recorded tmux argv: %q", args)
	return ""
}

// The wiring half: cmd's unit tests cover how the pane command is BUILT, this
// covers that the resolved TraceDir actually reaches the builder. A slip here
// is invisible to both the compiler and the cmd tests.
func TestTmuxSplitForwardsTheTraceDir(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on this host")
	}
	recorded := stubTmux(t)
	dir := filepath.Join(t.TempDir(), "trace")

	var errOut bytes.Buffer
	o := Options{
		BaseDir:     t.TempDir(),
		Args:        []string{"--height", "50%", "--trace-dir", dir},
		Defaults:    types.Config{Model: "m", BaseURL: "http://127.0.0.1:1/v1"},
		SkipSetup:   true,
		SkipBareEnv: true,
		Stderr:      &errOut,
	}
	runCtx(context.Background(), o)

	inner := innerCommandFrom(t, recorded)
	if !strings.Contains(inner, "--trace-dir '"+dir+"'") {
		t.Fatalf("the split pane would run untraced: %q", inner)
	}
}
