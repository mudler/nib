package mcp

import (
	"net/url"
	"strings"
)

// ddgBaseURL is the DuckDuckGo HTML endpoint. It is a package var so tests can
// point it at an httptest server.
var ddgBaseURL = "https://html.duckduckgo.com/html/"

// defaultSearchResults is returned when the caller does not specify max_results.
const defaultSearchResults = 5

// maxSearchResults caps how many results a single call may return.
const maxSearchResults = 25

type webSearchInput struct {
	Query      string `json:"query" jsonschema:"the search query"`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"max results to return (default 5, max 25)"`
}

type webSearchResult struct {
	Title   string `json:"title" jsonschema:"result title"`
	URL     string `json:"url" jsonschema:"result URL"`
	Snippet string `json:"snippet" jsonschema:"result snippet"`
}

type webSearchOutput struct {
	Query   string            `json:"query" jsonschema:"the query that was searched"`
	Results []webSearchResult `json:"results" jsonschema:"the search results"`
	Count   int               `json:"count" jsonschema:"number of results returned"`
	Error   string            `json:"error,omitempty" jsonschema:"error message if the search failed"`
}

// decodeDDGHref unwraps DuckDuckGo's /l/?uddg=<target> redirect link to recover
// the real target URL. Hrefs without a uddg parameter are returned unchanged.
func decodeDDGHref(href string) string {
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	if uddg := u.Query().Get("uddg"); uddg != "" {
		return uddg // url.Values.Get already returns the decoded value
	}
	return strings.TrimSpace(href)
}
