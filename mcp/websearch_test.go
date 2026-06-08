package mcp

import (
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
