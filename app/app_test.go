package app

import (
	"bytes"
	"strings"
	"testing"
)

func TestMainVersionReturnsZero(t *testing.T) {
	var out bytes.Buffer
	o := Options{Args: []string{"--version"}, Stdout: &out}
	if code := run(o); code != 0 {
		t.Fatalf("--version exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "nib") {
		t.Fatalf("--version output = %q, want it to mention nib", out.String())
	}
}

func TestMainInitScriptUsesProgramName(t *testing.T) {
	var out bytes.Buffer
	o := Options{Args: []string{"--init", "zsh"}, Stdout: &out, ProgramName: "local-ai chat"}
	if code := run(o); code != 0 {
		t.Fatalf("--init zsh exit code = %d, want 0", code)
	}
	if out.Len() == 0 {
		t.Fatal("--init zsh wrote nothing")
	}
}

func TestUnknownShellIsAnError(t *testing.T) {
	var errOut bytes.Buffer
	o := Options{Args: []string{"--init", "tcsh"}, Stderr: &errOut}
	if code := run(o); code == 0 {
		t.Fatal("--init tcsh exit code = 0, want non-zero")
	}
	if !strings.Contains(errOut.String(), "tcsh") {
		t.Fatalf("stderr = %q, want it to name the bad shell", errOut.String())
	}
}
