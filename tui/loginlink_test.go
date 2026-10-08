package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

const testLoginURL = "https://auth.openai.com/oauth/authorize?client_id=app_EMoamEEZ73f0CkXaXp7hrann&code_challenge=DoLhOQHdY4hl4Jnvk4pKSP7V1Ruu1V3mZ3wzBpgpnNI&code_challenge_method=S256&codex_cli_simplified_flow=true&id_token_add_organizations=true&originator=nib&redirect_uri=http%3A%2F%2F127.0.0.1%3A1455%2Fauth%2Fcallback&response_type=code&scope=openid+profile+email+offline_access+api.connectors.read+api.connectors.invoke&state=688d1269ad11d326a9bd99c83b104291"

// A login URL wider than the terminal must stay visible and complete: wrapped
// at the terminal width, never clipped. Clipping drops redirect_uri and the
// provider rejects the request.
func TestLoginPromptKeepsURLIntact(t *testing.T) {
	// The SSH prompt indents the URL; the plain one does not.
	for _, prompt := range []string{
		"Open this URL to log in:\n" + testLoginURL + "\n\nIf the browser cannot connect, paste the URL here.",
		"Then open this URL in your LOCAL browser:\n  " + testLoginURL + "\n\nIf you cannot set up port forwarding, paste it here.",
	} {
		checkLoginPrompt(t, prompt)
	}
}

func checkLoginPrompt(t *testing.T, prompt string) {
	t.Helper()
	for _, width := range []int{40, 80, 200} {
		out := renderLogin(loginPrompt(prompt, testLoginURL), width)
		for _, line := range strings.Split(out, "\n") {
			if w := ansi.StringWidth(line); w > width {
				t.Fatalf("width %d: line is %d columns and would be clipped: %q", width, w, line)
			}
		}
		// Hyperlink targets are not visible text; strip them to see what the
		// terminal shows, then undo the hard wrap.
		visible := strings.ReplaceAll(strings.ReplaceAll(ansi.Strip(out), "\n", ""), " ", "")
		if !strings.Contains(visible, testLoginURL) {
			t.Fatalf("width %d: visible URL is incomplete:\n%q", width, visible)
		}
		if !strings.Contains(out, ansi.SetHyperlink(testLoginURL)) {
			t.Fatalf("width %d: missing hyperlink", width)
		}
	}
}

func TestLoginPromptWithoutURLInText(t *testing.T) {
	out := loginPrompt("Enter code ABCD", testLoginURL)
	if !strings.Contains(out, testLoginURL) || !strings.Contains(out, "Enter code ABCD") {
		t.Fatalf("got %q", out)
	}
}
