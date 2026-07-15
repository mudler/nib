//go:build browserintegration

package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
	"github.com/mudler/nib/types"
)

// TestNavigateAndSnapshotAgainstRealChrome drives a real headed Chrome via
// chromedp against a local httptest server and asserts the resulting
// snapshot contains the page's interactive elements and their @e refs.
//
// Opt-in only (needs a real Chrome/Chromium on the machine):
//
//	go test -tags browserintegration ./mcp/ -run Real
func TestNavigateAndSnapshotAgainstRealChrome(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body><a href="/x">Home</a><input aria-label="Search box"><button>Go</button></body></html>`)
	}))
	defer srv.Close()

	b := newBrowserServer(types.BrowserConfig{AllowPrivateURLs: true, ProfileDir: t.TempDir() + "/p"})
	defer b.close()

	_, out, err := b.browserNavigate(context.Background(), nil, BrowserInput{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Snapshot, "Search box") || !strings.Contains(out.Snapshot, "@e") {
		t.Fatalf("snapshot missing the input/refs:\n%s", out.Snapshot)
	}
}

// TestClickAgainstRealChrome drives a real headed Chrome, grabs the ref for a
// link that changes document.title on click from the snapshot, clicks it via
// browserClick, and asserts the title actually changed.
//
// Opt-in only (needs a real Chrome/Chromium on the machine):
//
//	go test -tags browserintegration ./mcp/ -run Click
func TestClickAgainstRealChrome(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body><a href="#" onclick="document.title='clicked'; return false;">Click me</a></body></html>`)
	}))
	defer srv.Close()

	b := newBrowserServer(types.BrowserConfig{AllowPrivateURLs: true, ProfileDir: t.TempDir() + "/p"})
	defer b.close()

	_, out, err := b.browserNavigate(context.Background(), nil, BrowserInput{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	ref := refForText(out.Snapshot, "Click me")
	if ref == "" {
		t.Fatalf("no ref found for the link in snapshot:\n%s", out.Snapshot)
	}

	if _, _, err := b.browserClick(context.Background(), nil, BrowserInput{Ref: ref}); err != nil {
		t.Fatal(err)
	}

	var title string
	if err := chromedp.Run(b.bctx, chromedp.Title(&title)); err != nil {
		t.Fatal(err)
	}
	if title != "clicked" {
		t.Fatalf("title = %q, want %q", title, "clicked")
	}
}

// TestTypeAgainstRealChrome drives a real headed Chrome, grabs the ref for an
// <input> from the snapshot, types into it via browserType, and asserts the
// input's value in the DOM actually changed — the check the AX path (which
// never reaches the DOM) cannot pass.
//
// Opt-in only (needs a real Chrome/Chromium on the machine):
//
//	go test -tags browserintegration ./mcp/ -run Type
func TestTypeAgainstRealChrome(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body><input aria-label="Search box"></body></html>`)
	}))
	defer srv.Close()

	b := newBrowserServer(types.BrowserConfig{AllowPrivateURLs: true, ProfileDir: t.TempDir() + "/p"})
	defer b.close()

	_, out, err := b.browserNavigate(context.Background(), nil, BrowserInput{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	ref := refForText(out.Snapshot, "Search box")
	if ref == "" {
		t.Fatalf("no ref found for the input in snapshot:\n%s", out.Snapshot)
	}

	const want = "hello from CDP"
	if _, _, err := b.browserType(context.Background(), nil, BrowserInput{Ref: ref, Text: want}); err != nil {
		t.Fatal(err)
	}

	var got string
	if err := chromedp.Run(b.bctx, chromedp.Value("input", &got)); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("input value = %q, want %q", got, want)
	}
}

// refForText scans a rendered snapshot for the line containing text and
// returns its leading "@eN" ref, or "" if not found.
func refForText(snapshot, text string) string {
	for _, line := range strings.Split(snapshot, "\n") {
		if !strings.Contains(line, text) {
			continue
		}
		for _, tok := range strings.Fields(line) {
			if strings.HasPrefix(tok, "@e") {
				return tok
			}
		}
	}
	return ""
}
