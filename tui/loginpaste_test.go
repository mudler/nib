package tui

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/provider"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/tui/render/inline"
	"github.com/mudler/nib/types"
)

// startPasteLogin starts a real OAuth-code flow against a callback server on
// a free port, the way /login does, and returns it with the redirect URI and
// state a browser would come back with.
func startPasteLogin(t *testing.T) (flow *auth.LoginFlow, redirect, state string) {
	t.Helper()
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_TTY", "")
	def := provider.Definition{
		ID:                "test-oauth",
		Name:              "Test",
		LoginKind:         provider.LoginOAuthCode,
		CallbackPath:      "/auth/callback",
		AllowPortFallback: true,
		ClientID:          "client",
		AuthorizeURL:      "http://127.0.0.1:1/authorize",
	}
	store := auth.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	flow, err := auth.StartLogin(context.Background(), store, def)
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _ = flow.Complete(ctx)
	})
	u, err := url.Parse(flow.URL)
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	return flow, u.Query().Get("redirect_uri"), u.Query().Get("state")
}

func typeInto(t *testing.T, m Model, keys ...tea.KeyMsg) Model {
	t.Helper()
	for _, k := range keys {
		next, _ := m.Update(k)
		m = next.(Model)
	}
	return m
}

func paste(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s), Paste: true} }

var enter = tea.KeyMsg{Type: tea.KeyEnter}

// TestLoginWaitTakesAPastedRedirectURL is the regression test for /login
// with a browser that cannot reach the callback server. The dialog swallowed
// every key but esc and hid the composer, so there was nowhere to paste the
// URL, and outside SSH enter did nothing at all.
func TestLoginWaitTakesAPastedRedirectURL(t *testing.T) {
	flow, redirect, state := startPasteLogin(t)
	m := NewModel(context.Background(), types.Config{}, 40, nil, inline.New())
	m.loginWait = loginWait{active: true, entry: chat.ProviderEntry{ID: "test-oauth", Name: "Test"}, flow: flow}

	d := m.loginWait.dialog()
	if len(d.Options) == 0 || !strings.HasPrefix(d.Options[d.Selected].Text, theme.LoginWaitPasteLabel) {
		t.Fatalf("dialog has no field for the redirect URL: %+v", d)
	}

	m = typeInto(t, m, paste("not a url"), enter)
	if m.loginWait.note != theme.LoginWaitNotURL {
		t.Fatalf("note = %q, want %q", m.loginWait.note, theme.LoginWaitNotURL)
	}

	m = typeInto(t, m, tea.KeyMsg{Type: tea.KeyCtrlU}, paste(redirect+"?code=abc&state=wrong"), enter)
	if !strings.Contains(m.loginWait.note, "state mismatch") {
		t.Fatalf("a URL with the wrong state must be refused, note = %q", m.loginWait.note)
	}

	// A terminal can wrap a long paste; the spaces it adds are dropped.
	good := redirect + "?code=abc&state=" + state
	m = typeInto(t, m, tea.KeyMsg{Type: tea.KeyCtrlU}, paste(good[:20]+" \n"+good[20:]), enter)
	if m.loginWait.note != theme.LoginWaitPasted {
		t.Fatalf("note = %q, want %q", m.loginWait.note, theme.LoginWaitPasted)
	}
	if !m.loginWait.active {
		t.Fatal("the dialog must stay open until the flow reports its result")
	}
}

// TestLoginWaitWithoutPasteIgnoresTyping: a device-code flow has no redirect
// to paste, so typing must not show a field or change anything.
func TestLoginWaitWithoutPasteIgnoresTyping(t *testing.T) {
	m := NewModel(context.Background(), types.Config{}, 40, nil, inline.New())
	flow := auth.NewLoginFlow("dev", "Go to https://example.com and enter code: ABCD", "https://example.com", nil)
	m.loginWait = loginWait{active: true, entry: chat.ProviderEntry{ID: "dev", Name: "Dev"}, flow: flow, prompt: flow.Prompt}

	m = typeInto(t, m, paste("https://x"), enter)
	if len(m.loginWait.pasted) != 0 || m.loginWait.note != "" {
		t.Fatalf("device flow took input: %+v", m.loginWait)
	}
	if d := m.loginWait.dialog(); len(d.Options) != 1 || d.Hint != theme.LoginWaitHint {
		t.Fatalf("device flow dialog = %+v, want only the instructions", d)
	}
}
