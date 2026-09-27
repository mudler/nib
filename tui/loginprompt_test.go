package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/x/ansi"
)

// codexAuthorizeURL is the shape of a real openai-codex authorize URL: one
// token of about 440 characters, with dots, so a word-wrapper finds no place
// to break it that keeps it under a 200-column line.
const codexAuthorizeURL = "https://auth.openai.com/oauth/authorize?client_id=app_EMoamEEZ73f0CkXaXp7hrann&code_challenge=feDzYQmk3uS4DgGEHWqn55cXQ0bROJxw0X_ziXIGE_k&code_challenge_method=S256&codex_cli_simplified_flow=true&id_token_add_organizations=true&redirect_uri=http%3A%2F%2F127.0.0.1%3A1455%2Fauth%2Fcallback&response_type=code&scope=openid+profile+email+offline_access+api.connectors.read+api.connectors.invoke&state=adbc1bd257f1cb1927d7cd82fbfdeb32"

// visibleText is what a transcript shows once colors, links, line breaks
// and indentation are gone: the text a user could still read off it.
func visibleText(view string) string {
	var b strings.Builder
	for _, line := range strings.Split(ansi.Strip(view), "\n") {
		b.WriteString(strings.TrimSpace(line))
	}
	return b.String()
}

func transcriptModel(width int, msgs ...ChatMessage) Model {
	m := Model{
		viewport:  viewport.New(width, 60),
		width:     width,
		presenter: testPresenter(),
		messages:  msgs,
	}
	m.updateViewport()
	return m
}

// TestLongTokenIsNotClippedFromTheTranscript is the regression test for the
// lost middle of the /login URL. Glamour cannot break a token longer than
// its width, so it left a line wider than the viewport, and the viewport's
// MaxWidth cut the rest of that line off the screen.
func TestLongTokenIsNotClippedFromTheTranscript(t *testing.T) {
	for _, role := range []string{"agent", "assistant"} {
		m := transcriptModel(200, ChatMessage{Role: role, Content: "Open this URL in your LOCAL browser:\n  " + codexAuthorizeURL})
		if got := visibleText(m.viewport.View()); !strings.Contains(got, codexAuthorizeURL) {
			t.Fatalf("%s message lost part of the URL; visible text:\n%s", role, got)
		}
		for i, line := range strings.Split(m.viewport.View(), "\n") {
			if w := ansi.StringWidth(line); w > 200 {
				t.Fatalf("%s line %d is %d cells wide, wider than the 200-cell viewport", role, i, w)
			}
		}
	}
}

// TestPlainLoginPromptKeepsItsShape: the login prompt is shown as written.
// Rendered as markdown, its line breaks were reflowed into one paragraph and
// the "<user>@<this-host>" placeholder was stripped as HTML, leaving "@".
func TestPlainLoginPromptKeepsItsShape(t *testing.T) {
	prompt := "You appear to be connected over SSH.\n" +
		"Set up port forwarding from your LOCAL machine first:\n\n" +
		"  ssh -L 1455:127.0.0.1:1455 <user>@<this-host>\n\n" +
		"Then open this URL in your LOCAL browser:\n  " + codexAuthorizeURL
	m := transcriptModel(200, ChatMessage{Role: "agent", Content: prompt, Plain: true})
	view := m.viewport.View()
	plain := ansi.Strip(view)

	if !strings.Contains(plain, "<user>@<this-host>") {
		t.Fatalf("placeholder stripped from the prompt:\n%s", plain)
	}
	lines := strings.Split(plain, "\n")
	var sshLine, firstLine int = -1, -1
	for i, l := range lines {
		if strings.Contains(l, "You appear to be connected over SSH.") {
			firstLine = i
		}
		if strings.Contains(l, "ssh -L") {
			sshLine = i
		}
	}
	if firstLine < 0 || sshLine < 0 || strings.Contains(lines[firstLine], "Set up port forwarding") {
		t.Fatalf("the prompt's own line breaks were reflowed:\n%s", plain)
	}
	if got := visibleText(view); !strings.Contains(got, codexAuthorizeURL) {
		t.Fatalf("URL not shown whole; visible text:\n%s", got)
	}
	// Every piece of the wrapped URL links to the whole URL, so a click on
	// any line of it opens the right page.
	pieces := 0
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(ansi.Strip(l), "auth") || strings.Contains(ansi.Strip(l), "state=") {
			if strings.Contains(l, "8;id=") && strings.Contains(l, ";"+codexAuthorizeURL) {
				pieces++
			}
		}
	}
	if pieces < 2 {
		t.Fatalf("want each line of the wrapped URL to carry a hyperlink to the full URL, got %d:\n%q", pieces, view)
	}
	for i, line := range strings.Split(view, "\n") {
		if w := ansi.StringWidth(line); w > 200 {
			t.Fatalf("line %d is %d cells wide, wider than the 200-cell viewport", i, w)
		}
	}
}
