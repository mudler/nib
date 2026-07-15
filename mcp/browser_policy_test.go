package mcp

import "testing"

func TestCheckURLAllowedBlocksPrivate(t *testing.T) {
	for _, u := range []string{"http://localhost/", "http://127.0.0.1:8080", "http://192.168.1.5/", "http://10.0.0.1", "http://[::1]/", "http://foo.local/"} {
		if err := checkURLAllowed(u, false); err == nil {
			t.Fatalf("%s must be blocked when allowPrivate=false", u)
		}
		if err := checkURLAllowed(u, true); err != nil {
			t.Fatalf("%s must be allowed when allowPrivate=true: %v", u, err)
		}
	}
	for _, u := range []string{"https://www.imdb.com/", "https://example.com/x?q=1"} {
		if err := checkURLAllowed(u, false); err != nil {
			t.Fatalf("public %s must be allowed: %v", u, err)
		}
	}
	if err := checkURLAllowed("ftp://example.com", false); err == nil {
		t.Fatal("non-http scheme must be rejected")
	}
}
