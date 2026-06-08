package mcp

import "testing"

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
