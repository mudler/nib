package mcp

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// checkURLAllowed is the SSRF guard for browser_navigate: it rejects
// navigation to localhost/RFC1918/link-local/unspecified targets unless
// allowPrivate is set.
//
// Limitation (v1): hostnames are only checked by literal form (IP literal —
// including the alternate numeric encodings below — "localhost", or a
// "*.local" mDNS-style name). We deliberately do not DNS-resolve hostnames
// here, so a public-looking hostname that resolves to a private/loopback
// address (DNS rebinding) is not caught. Revisit if/when this tool is
// exposed to untrusted input at higher trust levels.
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

	// Normalize before any comparison: browsers (and the underlying resolver)
	// strip a single trailing "." (the DNS root) and treat hostnames
	// case-insensitively — do the same here, or "localhost." / "LOCALHOST"
	// slip past the checks below unblocked.
	host = strings.TrimSuffix(host, ".")
	lower := strings.ToLower(host)

	if ip := net.ParseIP(host); ip != nil {
		if isPrivateOrLoopback(ip) {
			return fmt.Errorf("navigation to private/loopback address %q is blocked", host)
		}
		return nil
	}

	if lower == "localhost" || strings.HasSuffix(lower, ".local") {
		return fmt.Errorf("navigation to local host %q is blocked", host)
	}

	// Alternate numeric IP encodings. net.ParseIP above only accepts a
	// canonical dotted-quad or bracketed IPv6 literal, but Chrome itself
	// still normalizes forms like "2130706433", "0x7f000001", or
	// "0177.0.0.1" down to a literal IP address before connecting — so a
	// host that fails ParseIP can still land on 127.0.0.1. No DNS lookup is
	// involved in any of this; it's pure string canonicalization of the host
	// the model handed us.
	if looksLikePackedInteger(lower) {
		// A bare integer/hex host (no dots, no colons) has no legitimate use
		// as a browser_navigate target — block it outright as a private-IP
		// encoding evasion attempt, regardless of what it decodes to.
		return fmt.Errorf("navigation to numeric-encoded host %q is blocked", host)
	}
	if ip, ok := decodeOctalDottedIP(lower); ok && isPrivateOrLoopback(ip) {
		return fmt.Errorf("navigation to private/loopback address %q is blocked", host)
	}

	return nil
}

// isPrivateOrLoopback reports whether ip is a loopback/private/link-local/
// unspecified address — the set browser_navigate blocks by default.
func isPrivateOrLoopback(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

// looksLikePackedInteger reports whether host (already lower-cased, and
// already known not to be a valid net.ParseIP literal) is a bare
// packed-integer IPv4 encoding: all decimal digits ("2130706433"), or a
// "0x"-prefixed hex string ("0x7f000001"), with no dots or colons — both
// decode to 127.0.0.1 in a real browser.
func looksLikePackedInteger(host string) bool {
	if host == "" || strings.ContainsAny(host, ".:") {
		return false
	}
	if hex, ok := strings.CutPrefix(host, "0x"); ok {
		return hex != "" && isAllHex(hex)
	}
	return isAllDigits(host)
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isAllHex(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') && !(r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

// decodeOctalDottedIP handles a 4-label dotted host where every label is
// numeric but the whole thing isn't a canonical net.ParseIP literal — the
// telltale sign of an octal- or hex-encoded octet, e.g. "0177.0.0.1"
// (127.0.0.1: Go's net.ParseIP rejects the leading-zero octet as ambiguous,
// but a browser resolves it as octal). Each label is parsed with
// strconv.ParseInt base 0, which honors Go's usual integer-literal prefixes
// (a "0" prefix means octal, "0x" means hex, otherwise decimal). Returns
// ok=false if the host isn't a 4-label all-numeric form, in which case it's
// just an ordinary hostname.
func decodeOctalDottedIP(host string) (net.IP, bool) {
	labels := strings.Split(host, ".")
	if len(labels) != 4 {
		return nil, false
	}
	octets := make([]byte, 4)
	for i, l := range labels {
		v, err := strconv.ParseInt(l, 0, 32)
		if err != nil || v < 0 || v > 255 {
			return nil, false
		}
		octets[i] = byte(v)
	}
	return net.IPv4(octets[0], octets[1], octets[2], octets[3]), true
}
