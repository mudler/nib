package auth

import (
	"strings"
	"testing"
)

// TestSSHHintNeverShowsABareAt: with SSH_CONNECTION missing (a shell inside
// tmux or sudo keeps only SSH_TTY) the hint used to fall back to
// "<user>@<this-host>", which the TUI's markdown then stripped to "@".
func TestSSHHintNeverShowsABareAt(t *testing.T) {
	t.Setenv("USER", "alice")

	t.Setenv("SSH_CONNECTION", "10.0.0.5 51000 192.168.1.20 22")
	if got := sshPortForwardHint(1455); got != "ssh -L 1455:127.0.0.1:1455 alice@192.168.1.20" {
		t.Fatalf("hint = %q", got)
	}

	t.Setenv("SSH_CONNECTION", "")
	got := sshPortForwardHint(1455)
	if !strings.HasPrefix(got, "ssh -L 1455:127.0.0.1:1455 alice@") || strings.HasSuffix(got, "@") {
		t.Fatalf("hint without SSH_CONNECTION = %q, want alice@<hostname>", got)
	}

	t.Setenv("USER", "")
	if got := sshUserHost(); strings.HasPrefix(got, "@") {
		t.Fatalf("user@host without USER = %q, want a user name or placeholder", got)
	}
}

// TestSSHPromptNamesTheRedirectHost: openai-codex redirects to 127.0.0.1
// since #127, so the prompt must not tell the user to look for localhost.
func TestSSHPromptNamesTheRedirectHost(t *testing.T) {
	p := sshOAuthPrompt("https://example.com/auth", "ssh -L 1455:127.0.0.1:1455 a@b", "127.0.0.1", 1455)
	if !strings.Contains(p, "redirect to\n127.0.0.1:1455)") {
		t.Fatalf("prompt does not name the redirect host:\n%s", p)
	}
}

func TestOAuthFlowRedirectHost(t *testing.T) {
	for uri, want := range map[string]string{
		"http://127.0.0.1:1455/auth/callback": "127.0.0.1",
		"http://localhost:8080/callback":      "localhost",
		"":                                    "localhost",
	} {
		if got := (&OAuthFlow{redirectURI: uri}).RedirectHost(); got != want {
			t.Errorf("RedirectHost(%q) = %q, want %q", uri, got, want)
		}
	}
}
