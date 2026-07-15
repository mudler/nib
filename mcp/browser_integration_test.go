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
	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// TestScrollAgainstRealChrome drives a real headed Chrome against a tall
// page, scrolls down via browserScroll, and asserts window.scrollY actually
// advanced.
//
// Opt-in only (needs a real Chrome/Chromium on the machine):
//
//	go test -tags browserintegration ./mcp/ -run Scroll
func TestScrollAgainstRealChrome(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body style="height:6000px"><h1>top</h1></body></html>`)
	}))
	defer srv.Close()

	b := newBrowserServer(types.BrowserConfig{AllowPrivateURLs: true, ProfileDir: t.TempDir() + "/p"})
	defer b.close()

	if _, _, err := b.browserNavigate(context.Background(), nil, BrowserInput{URL: srv.URL}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := b.browserScroll(context.Background(), nil, BrowserInput{Direction: "down"}); err != nil {
		t.Fatal(err)
	}

	var scrollY float64
	if err := chromedp.Run(b.bctx, chromedp.Evaluate("window.scrollY", &scrollY)); err != nil {
		t.Fatal(err)
	}
	if scrollY <= 0 {
		t.Fatalf("scrollY = %v, want > 0 after scrolling down", scrollY)
	}
}

// TestPressEnterSubmitsFormAgainstRealChrome drives a real headed Chrome
// against a page with a GET form, types into its input via browserType,
// presses Enter via browserPress, and asserts the resulting navigation (the
// query string lands in the URL) actually happened.
//
// Opt-in only (needs a real Chrome/Chromium on the machine):
//
//	go test -tags browserintegration ./mcp/ -run PressEnter
func TestPressEnterSubmitsFormAgainstRealChrome(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			fmt.Fprint(w, `<html><head><title>submitted</title></head><body>ok</body></html>`)
			return
		}
		fmt.Fprint(w, `<html><body><form action="" method="GET"><input name="q" aria-label="Query box"></form></body></html>`)
	}))
	defer srv.Close()

	b := newBrowserServer(types.BrowserConfig{AllowPrivateURLs: true, ProfileDir: t.TempDir() + "/p"})
	defer b.close()

	_, out, err := b.browserNavigate(context.Background(), nil, BrowserInput{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	ref := refForText(out.Snapshot, "Query box")
	if ref == "" {
		t.Fatalf("no ref found for the input in snapshot:\n%s", out.Snapshot)
	}

	if _, _, err := b.browserType(context.Background(), nil, BrowserInput{Ref: ref, Text: "hello"}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := b.browserPress(context.Background(), nil, BrowserInput{Key: "Enter"}); err != nil {
		t.Fatal(err)
	}

	var title string
	if err := chromedp.Run(b.bctx, chromedp.Title(&title)); err != nil {
		t.Fatal(err)
	}
	if title != "submitted" {
		t.Fatalf("title = %q, want %q (form should have submitted on Enter)", title, "submitted")
	}
}

// TestVisionAgainstRealChrome drives a real headed Chrome, calls
// browserVision, and asserts the result carries an *mcp.ImageContent with
// non-empty PNG data alongside the "screenshot for: <question>" text.
//
// Opt-in only (needs a real Chrome/Chromium on the machine):
//
//	go test -tags browserintegration ./mcp/ -run Vision
func TestVisionAgainstRealChrome(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body><h1>hello</h1></body></html>`)
	}))
	defer srv.Close()

	b := newBrowserServer(types.BrowserConfig{AllowPrivateURLs: true, ProfileDir: t.TempDir() + "/p"})
	defer b.close()

	if _, _, err := b.browserNavigate(context.Background(), nil, BrowserInput{URL: srv.URL}); err != nil {
		t.Fatal(err)
	}

	res, _, err := b.browserVision(context.Background(), nil, BrowserInput{Question: "what is on the page?"})
	if err != nil {
		t.Fatal(err)
	}

	var img *mcp.ImageContent
	var gotText bool
	for _, c := range res.Content {
		switch ct := c.(type) {
		case *mcp.ImageContent:
			img = ct
		case *mcp.TextContent:
			if strings.Contains(ct.Text, "screenshot for: what is on the page?") {
				gotText = true
			}
		}
	}
	if img == nil {
		t.Fatal("expected an *mcp.ImageContent in the result, got none")
	}
	if len(img.Data) == 0 {
		t.Fatal("screenshot data is empty")
	}
	if img.MIMEType != "image/png" {
		t.Fatalf("MIMEType = %q, want image/png", img.MIMEType)
	}
	if !gotText {
		t.Fatal("expected a text content naming the question, got none")
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
