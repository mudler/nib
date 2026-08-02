package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The tmux split is not a message, it is a process nib spawns to be itself
// again. Standalone it must be exactly what it always was: the resolved
// executable path, bare, followed by the two flags and the redirect.
func TestTmuxInnerCommandStandaloneIsUnchanged(t *testing.T) {
	const (
		exe  = "/home/u/go/bin/nib"
		want = `/home/u/go/bin/nib --height 50% --no-tmux > '/tmp/nib-yank-42'; tmux wait-for -S 'nib-nib-yank-42'`
	)
	// An empty program name and the explicit "nib" are the same thing, the same
	// way they are for the --init scripts.
	for _, prog := range []string{"", "nib"} {
		got := tmuxInnerCommand(prog, exe, "50%", "/tmp/nib-yank-42", "nib-nib-yank-42")
		if got != want {
			t.Fatalf("program name %q:\n got  %s\n want %s", prog, got, want)
		}
	}
}

// The bug. An embedder is reached as a SUBCOMMAND, so the executable path alone
// is not the invocation: os.Executable() gives LocalAI /usr/bin/local-ai, and
// spawning that without `chat` does not fail cleanly, because `run` is
// LocalAI's default command. The pane would try to boot a server.
func TestTmuxInnerCommandKeepsTheEmbedderSubcommand(t *testing.T) {
	got := tmuxInnerCommand("local-ai chat", "/usr/bin/local-ai", "50%", "/tmp/o", "ch")
	if !strings.HasPrefix(got, "/usr/bin/local-ai chat --height 50% --no-tmux ") {
		t.Fatalf("the pane does not re-enter the embedded agent: %s", got)
	}
}

// A three-word program name keeps all of its subcommand words, in order. One
// dropped word is the same failure as the reported one.
func TestTmuxInnerCommandKeepsEverySubcommandWord(t *testing.T) {
	got := tmuxInnerCommand("myprog ai agent", "/opt/bin/myprog", "20", "/tmp/o", "ch")
	if !strings.HasPrefix(got, "/opt/bin/myprog ai agent --height 20 ") {
		t.Fatalf("subcommand words were dropped or reordered: %s", got)
	}
}

// The words go into an `sh -c` string, so they are quoted per word rather than
// pasted, the same rule initScriptCommand follows. Quoting the invocation as a
// unit would instead make sh look for one command with a space in its name.
func TestTmuxInnerCommandQuotesPerWord(t *testing.T) {
	got := tmuxInnerCommand("prog sub;rm -rf /", "/opt/my prog/bin", "20", "/tmp/o", "ch")
	if !strings.HasPrefix(got, `'/opt/my prog/bin' 'sub;rm' -rf / --height 20 `) {
		t.Fatalf("words were not quoted individually: %s", got)
	}
}

// os.Executable() can fail. Standalone that has always meant the bare name and
// a PATH lookup; embedded, the program name is the only thing that can still
// name the right binary, so it has to be the fallback rather than "nib".
func TestTmuxInnerCommandFallsBackToTheProgramName(t *testing.T) {
	if got := tmuxInnerCommand("", "", "50%", "/tmp/o", "ch"); !strings.HasPrefix(got, "nib --height ") {
		t.Fatalf("standalone fallback changed: %s", got)
	}
	got := tmuxInnerCommand("local-ai chat", "", "50%", "/tmp/o", "ch")
	if !strings.HasPrefix(got, "local-ai chat --height ") {
		t.Fatalf("embedded fallback does not name the embedder: %s", got)
	}
}

// The string is not just shaped right, it RUNS right. A stand-in executable
// records the argv it was handed, so this proves the pane's process receives
// `chat` as its own argument rather than as part of the program's name, and
// that the redirect and the quoting survive a real shell.
func TestTmuxInnerCommandExecutesAsTheRightArgv(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on this host")
	}
	dir := t.TempDir()
	// A stand-in for the host binary: it prints its arguments, which is what
	// the real inner nib would print as the captured shell command.
	exe := filepath.Join(dir, "local-ai")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "capture out")

	inner := tmuxInnerCommand("local-ai chat", exe, "50%", outPath, "unused-channel")
	// tmux is absent here, so the trailing wait-for signal fails; the redirect
	// before it is what this asserts, and a non-zero exit from the tail is not
	// a failure of the construction.
	_ = exec.Command(sh, "-c", inner).Run()

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("the pane command never wrote the capture file: %v", err)
	}
	got := strings.Fields(string(data))
	want := []string{"chat", "--height", "50%", "--no-tmux"}
	if len(got) != len(want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv = %q, want %q", got, want)
		}
	}
}
