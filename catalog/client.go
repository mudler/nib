package catalog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client fetches and merges catalog sources over HTTP. RawBase and APIBase are
// fields so tests can point them at an httptest server; production uses
// NewClient's public GitHub endpoints.
type Client struct {
	HTTP    *http.Client
	RawBase string // raw file host, default https://raw.githubusercontent.com
	APIBase string // GitHub API host, default https://api.github.com
	// Branches is the ordered list of default branches to try for github
	// index-first resolution and crawl.
	Branches []string
}

// NewClient returns a Client with production GitHub endpoints and a 30s timeout.
func NewClient() *Client {
	return &Client{
		HTTP:     &http.Client{Timeout: 30 * time.Second},
		RawBase:  "https://raw.githubusercontent.com",
		APIBase:  "https://api.github.com",
		Branches: []string{"main", "master"},
	}
}

// Fetch resolves a single source to its metas. It never stamps provenance —
// that happens in Merge. A bundled source reads its embedded index; every other
// kind fetches over HTTP.
func (c *Client) Fetch(ctx context.Context, s Source) ([]Meta, error) {
	switch s.Kind {
	case SourceBundled:
		return ParseIndex(bundledIndex)
	case SourceIndex:
		return c.fetchIndex(ctx, s.URL)
	case SourceWellKnown:
		return c.fetchIndex(ctx, wellKnownURL(s.URL))
	case SourceGitHub:
		return c.fetchGitHub(ctx, s.URL)
	default:
		return nil, fmt.Errorf("unknown source kind %q", s.Kind)
	}
}

// fetchIndex GETs url and parses it as an index document.
func (c *Client) fetchIndex(ctx context.Context, url string) ([]Meta, error) {
	data, err := c.getBytes(ctx, url, 10<<20)
	if err != nil {
		return nil, err
	}
	return ParseIndex(data)
}

// fetchGitHub resolves a github source index-first: it tries each candidate
// branch's .well-known index then its repo-root index.json; only if none exists
// does it fall back to crawling the tree (githubCrawl, Task 4).
func (c *Client) fetchGitHub(ctx context.Context, url string) ([]Meta, error) {
	owner, repo, ok := parseGitHubRepo(url)
	if !ok {
		return nil, fmt.Errorf("not a github repo url: %q", url)
	}
	for _, br := range c.Branches {
		for _, p := range []string{".well-known/skills/index.json", "index.json"} {
			raw := fmt.Sprintf("%s/%s/%s/%s/%s", c.RawBase, owner, repo, br, p)
			if data, err := c.getBytes(ctx, raw, 10<<20); err == nil {
				return ParseIndex(data)
			}
		}
	}
	return c.githubCrawl(ctx, owner, repo)
}

// wellKnownURL builds the agentskills.io discovery URL for a bare host, adding a
// scheme if the user omitted one.
func wellKnownURL(host string) string {
	h := strings.TrimSuffix(strings.TrimSpace(host), "/")
	if !strings.Contains(h, "://") {
		h = "https://" + h
	}
	return h + "/.well-known/skills/index.json"
}

// getBytes performs a context-bound GET, returning the body (bounded to limit
// bytes) or an error for any non-200 status.
func (c *Client) getBytes(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get %s: status %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}
