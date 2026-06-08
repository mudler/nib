package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestDecodeDDGHref(t *testing.T) {
	tests := []struct {
		name string
		href string
		want string
	}{
		{
			name: "uddg redirect wrapper is decoded",
			href: "//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2F&rut=abc",
			want: "https://go.dev/doc/",
		},
		{
			name: "relative uddg wrapper",
			href: "/l/?uddg=https%3A%2F%2Fexample.com%2Fa%3Fb%3Dc",
			want: "https://example.com/a?b=c",
		},
		{
			name: "plain href without uddg passes through",
			href: "https://plain.example.com/page",
			want: "https://plain.example.com/page",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decodeDDGHref(tt.href); got != tt.want {
				t.Errorf("decodeDDGHref(%q) = %q, want %q", tt.href, got, tt.want)
			}
		})
	}
}

func TestParseDDGResults(t *testing.T) {
	body, err := os.ReadFile("testdata/ddg_golang.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	t.Run("parses title, decoded url, and snippet", func(t *testing.T) {
		got, err := parseDDGResults(string(body), 10)
		if err != nil {
			t.Fatalf("parseDDGResults: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("expected 3 results, got %d", len(got))
		}
		first := got[0]
		if first.Title != "The Go Programming Language" {
			t.Errorf("title = %q", first.Title)
		}
		if first.URL != "https://go.dev/" {
			t.Errorf("url = %q, want decoded https://go.dev/", first.URL)
		}
		if first.Snippet != "Build simple, secure, scalable systems with Go." {
			t.Errorf("snippet = %q", first.Snippet)
		}
	})

	t.Run("respects max results", func(t *testing.T) {
		got, err := parseDDGResults(string(body), 2)
		if err != nil {
			t.Fatalf("parseDDGResults: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 results, got %d", len(got))
		}
	})
}

func TestSearchWebHandler(t *testing.T) {
	body, err := os.ReadFile("testdata/ddg_golang.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "golang" {
			t.Errorf("query q = %q, want golang", got)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	old := ddgBaseURL
	ddgBaseURL = srv.URL + "/"
	defer func() { ddgBaseURL = old }()

	_, out, err := searchWeb(context.Background(), nil, webSearchInput{Query: "golang"})
	if err != nil {
		t.Fatalf("searchWeb returned error: %v", err)
	}
	if out.Error != "" {
		t.Fatalf("output error: %s", out.Error)
	}
	if out.Count != 3 || len(out.Results) != 3 {
		t.Fatalf("expected 3 results, got count=%d len=%d", out.Count, len(out.Results))
	}
	if out.Results[0].URL != "https://go.dev/" {
		t.Errorf("first url = %q", out.Results[0].URL)
	}
}

func TestSearchWebDefaultsAndCaps(t *testing.T) {
	if got := normalizeMaxResults(0); got != defaultSearchResults {
		t.Errorf("normalizeMaxResults(0) = %d, want %d", got, defaultSearchResults)
	}
	if got := normalizeMaxResults(1000); got != maxSearchResults {
		t.Errorf("normalizeMaxResults(1000) = %d, want %d", got, maxSearchResults)
	}
	if got := normalizeMaxResults(3); got != 3 {
		t.Errorf("normalizeMaxResults(3) = %d, want 3", got)
	}
}
