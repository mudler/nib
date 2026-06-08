package mcp

import (
	"net/url"
	"strings"

	"golang.org/x/net/html"
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

// nodeText returns the concatenated, whitespace-collapsed text of a node subtree.
func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(c *html.Node) {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
		for ch := c.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

// attr returns the value of the named attribute, or "".
func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

// hasClass reports whether the node's class attribute contains the given token.
func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

// parseDDGResults extracts up to max results from a DuckDuckGo HTML response.
// Titles/URLs come from <a class="result__a">, snippets from
// <* class="result__snippet">; the two are zipped by document order.
func parseDDGResults(body string, max int) ([]webSearchResult, error) {
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil, err
	}

	type link struct{ title, url string }
	var links []link
	var snippets []string

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch {
			case n.Data == "a" && hasClass(n, "result__a"):
				links = append(links, link{
					title: nodeText(n),
					url:   decodeDDGHref(attr(n, "href")),
				})
			case hasClass(n, "result__snippet"):
				snippets = append(snippets, nodeText(n))
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	var out []webSearchResult
	for i, l := range links {
		if l.title == "" || l.url == "" {
			continue
		}
		r := webSearchResult{Title: l.title, URL: l.url}
		if i < len(snippets) {
			r.Snippet = snippets[i]
		}
		out = append(out, r)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out, nil
}
