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

// TestCheckURLAllowedBlocksAlternateEncodings covers the SSRF-guard bypasses
// from the whole-branch review: alternate numeric IP encodings (decimal,
// 0x-hex, octal-dotted) and trailing-dot hostnames all still resolve to
// 127.0.0.1 in a real browser even though net.ParseIP rejects them, so they
// must be blocked the same as the canonical forms.
func TestCheckURLAllowedBlocksAlternateEncodings(t *testing.T) {
	for _, u := range []string{
		"http://2130706433/", // decimal packed 127.0.0.1
		"http://0x7f000001/", // hex packed 127.0.0.1
		"http://0177.0.0.1/", // octal-dotted 127.0.0.1
		"http://0/",          // decimal packed 0.0.0.0
		"http://localhost./", // trailing-dot localhost
		"http://127.0.0.1./", // trailing-dot loopback literal
	} {
		if err := checkURLAllowed(u, false); err == nil {
			t.Errorf("%s must be blocked when allowPrivate=false", u)
		}
		if err := checkURLAllowed(u, true); err != nil {
			t.Errorf("%s must be allowed when allowPrivate=true: %v", u, err)
		}
	}
}
