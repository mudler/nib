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

// loginPrompt returns the transcript text for a login flow: the prompt with
// its URL, plus a clickable OSC 8 link for terminals that support it. The raw
// URL stays visible because over SSH or in a terminal without hyperlinks it is
// the only way to reach the login page.
func loginPrompt(prompt, url string) string {
	if url == "" {
		return prompt
	}
	link := ansi.SetHyperlink(url) + loginLinkLabel + ansi.ResetHyperlink()
	if !strings.Contains(prompt, url) {
		prompt += "\n" + url
	}
	return prompt + "\n\n" + link
}

// renderLogin lays out a login prompt without Markdown. A URL is wider than
// most terminals, and word-wrapping or clipping it drops characters, so lines
// without spaces are hard-wrapped at exactly width columns. Browsers drop the
// newlines when such a URL is pasted.
func renderLogin(content string, width int) string {
	if width < 1 {
		return content
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if !strings.ContainsAny(line, " \t") && ansi.StringWidth(line) > width {
			lines[i] = ansi.Hardwrap(line, width, true)
			continue
		}
		lines[i] = strings.TrimRight(render.Wrap(line, width), "\n")
	}
	return strings.Join(lines, "\n")
}
