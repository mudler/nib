package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTMLToText(t *testing.T) {
	in := `<html><head><title>T</title><style>.x{}</style></head>
	<body><h1>Hello</h1><script>ignore()</script><p>World  of   Go</p></body></html>`
	got := htmlToText(in)
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "World of Go") {
		t.Errorf("expected visible text, got %q", got)
	}
	if strings.Contains(got, "ignore()") || strings.Contains(got, ".x{}") {
		t.Errorf("script/style content leaked into output: %q", got)
	}
}

func TestFetchURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body><p>fetched body text</p></body></html>"))
	}))
	defer srv.Close()

	text, finalURL, truncated, err := fetchURL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetchURL: %v", err)
	}
	if !strings.Contains(text, "fetched body text") {
		t.Errorf("text = %q", text)
	}
	if truncated {
		t.Errorf("did not expect truncation for a tiny body")
	}
	if finalURL == "" {
		t.Errorf("expected a final URL")
	}
}

func TestFetchURLTruncates(t *testing.T) {
	big := "<html><body>" + strings.Repeat("word ", fetchMaxContentRune) + "</body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(big))
	}))
	defer srv.Close()

	text, _, truncated, err := fetchURL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetchURL: %v", err)
	}
	if !truncated {
		t.Errorf("expected truncation for an oversized body")
	}
	if len([]rune(text)) > fetchMaxContentRune {
		t.Errorf("text exceeds cap: %d runes", len([]rune(text)))
	}
}
