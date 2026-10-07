package tui

import (
	"os"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
	"github.com/mudler/nib/tui/render"
	"github.com/muesli/termenv"
)

// loginLinkLabel is the visible text of the clickable login link.
const loginLinkLabel = "Open the login page"

// copyToClipboard puts s on the clipboard. It writes the OSC 52 sequence, which
// reaches the local clipboard through SSH and tmux, and also asks the OS
// clipboard for the case where the terminal ignores OSC 52. Failures are
// ignored: the clickable link is still shown.
func copyToClipboard(s string) {
	termenv.NewOutput(os.Stdout).Copy(s)
	_ = clipboard.WriteAll(s)
}

// loginPrompt returns the transcript text for a login flow. A login URL is far
// wider than most terminals, so printing it as text lets Markdown word-wrap
// break it and the terminal clip it, and a copied URL then loses characters. The
// URL is instead shown as an OSC 8 hyperlink with a short label.
func loginPrompt(prompt, url string) string {
	if url == "" {
		return prompt
	}
	link := ansi.SetHyperlink(url) + loginLinkLabel + ansi.ResetHyperlink()
	if !strings.Contains(prompt, url) {
		return prompt + "\n" + link
	}
	return strings.ReplaceAll(prompt, url, link+" (URL sent to your clipboard)")
}

// renderLogin lays out a login prompt without Markdown. Each source line is
// wrapped on word boundaries, and the hyperlink is a single short word, so the
// URL never wraps.
func renderLogin(content string, width int) string {
	return render.Wrap(content, width)
}
