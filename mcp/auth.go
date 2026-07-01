package mcp

import (
	"net/http"

	"github.com/mudler/nib/types"
)

// headerRoundTripper injects a fixed set of headers into every request before
// delegating to base.
type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	return t.base.RoundTrip(req)
}

// authenticatedHTTPClient returns an *http.Client that injects srv's bearer
// token and custom headers into every request, or nil if srv has neither set
// (letting the SDK fall back to http.DefaultClient — unchanged behavior for
// existing unauthenticated remote servers).
func authenticatedHTTPClient(srv types.MCPServer) *http.Client {
	if srv.BearerToken == "" && len(srv.Headers) == 0 {
		return nil
	}
	headers := make(map[string]string, len(srv.Headers)+1)
	for k, v := range srv.Headers {
		headers[k] = v
	}
	if srv.BearerToken != "" {
		headers["Authorization"] = "Bearer " + srv.BearerToken
	}
	return &http.Client{
		Transport: &headerRoundTripper{base: http.DefaultTransport, headers: headers},
	}
}
