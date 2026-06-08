package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	fetchTimeoutSeconds = 30
	fetchMaxBodyBytes   = 2 << 20 // 2 MiB
	fetchMaxContentRune = 100_000 // chars of extracted text fed to the LLM
	fetchMaxRedirects   = 10
)

// htmlToText extracts readable text from an HTML document, skipping script,
// style, noscript and head subtrees, and collapsing whitespace.
func htmlToText(body string) string {
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		// Not parseable as HTML: fall back to the raw body.
		return strings.Join(strings.Fields(body), " ")
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "head":
				return
			}
		}
		if n.Type == html.TextNode {
			if t := strings.TrimSpace(n.Data); t != "" {
				b.WriteString(t)
				b.WriteString(" ")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return strings.Join(strings.Fields(b.String()), " ")
}

// fetchURL GETs url (permissive: any host), extracts readable text, and
// truncates it to fetchMaxContentRune. Returns the text, the final URL after
// redirects, and whether truncation occurred.
func fetchURL(ctx context.Context, target string) (text, finalURL string, truncated bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeoutSeconds*time.Second)
	defer cancel()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= fetchMaxRedirects {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", "", false, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; wiz/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", resp.Request.URL.String(), false, fmt.Errorf("status %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, fetchMaxBodyBytes))
	if err != nil {
		return "", resp.Request.URL.String(), false, err
	}

	ct := resp.Header.Get("Content-Type")
	var content string
	if strings.Contains(ct, "html") {
		content = htmlToText(string(raw))
	} else {
		content = strings.Join(strings.Fields(string(raw)), " ")
	}

	if runes := []rune(content); len(runes) > fetchMaxContentRune {
		content = string(runes[:fetchMaxContentRune])
		truncated = true
	}
	return content, resp.Request.URL.String(), truncated, nil
}
