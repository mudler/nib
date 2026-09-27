package auth

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/provider"
)

// TestLocalOAuthLoginAcceptsPastedURL: outside SSH the browser can still
// fail to reach the callback server (another machine, a sandbox, a
// firewall), so a local OAuth-code flow must offer the paste fallback too.
func TestLocalOAuthLoginAcceptsPastedURL(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_TTY", "")
	flow := startTestOAuthLogin(t)

	if flow.IsSSH {
		t.Fatal("flow reports SSH with SSH_CONNECTION and SSH_TTY unset")
	}
	if !flow.AcceptsPastedURL() {
		t.Fatal("a local OAuth-code flow must accept a pasted redirect URL")
	}
	if !strings.Contains(flow.Prompt, "paste it here") {
		t.Fatalf("prompt does not mention the paste fallback: %q", flow.Prompt)
	}
}

func TestDeviceStyleFlowDoesNotAcceptPastedURL(t *testing.T) {
	flow := NewLoginFlow("x", "Go to … and enter code", "", nil)
	if flow.AcceptsPastedURL() {
		t.Fatal("a flow without a callback server cannot take a pasted URL")
	}
}

// startTestOAuthLogin starts an OAuth-code login against a callback server
// on a free port. Nothing reaches the network: the token exchange only runs
// if Complete receives a code, and the cleanup cancels it first.
func startTestOAuthLogin(t *testing.T) *LoginFlow {
	t.Helper()
	def := provider.Definition{
		ID:                "test-oauth",
		Name:              "Test",
		LoginKind:         provider.LoginOAuthCode,
		CallbackPort:      0,
		CallbackPath:      "/auth/callback",
		AllowPortFallback: true,
		ClientID:          "client",
		AuthorizeURL:      "http://127.0.0.1:1/authorize",
	}
	store := NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	flow, err := StartLogin(context.Background(), store, def)
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _ = flow.Complete(ctx) // shuts the callback server down
	})
	return flow
}
