package mcp

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// checkURLAllowed is the SSRF guard for browser_navigate: it rejects
// navigation to localhost/RFC1918/link-local/unspecified targets unless
// allowPrivate is set.
//
// Limitation (v1): hostnames are only checked by literal form (IP literal,
// "localhost", or a "*.local" mDNS-style name). We deliberately do not
// DNS-resolve hostnames here, so a public-looking hostname that resolves to
// a private/loopback address (DNS rebinding) is not caught. Revisit if/when
// this tool is exposed to untrusted input at higher trust levels.
func checkURLAllowed(rawURL string, allowPrivate bool) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}

	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("URL scheme %q is not allowed (only http/https)", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL %q has no host", rawURL)
	}

	if allowPrivate {
		return nil
	}

	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return fmt.Errorf("navigation to private/loopback address %q is blocked", host)
		}
		return nil
	}

	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".local") {
		return fmt.Errorf("navigation to local host %q is blocked", host)
	}

	return nil
}
