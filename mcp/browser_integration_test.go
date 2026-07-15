//go:build browserintegration

package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

// TestNavigateAndSnapshotAgainstRealChrome drives a real headed Chrome via
// chromedp against a local httptest server and asserts the resulting
// snapshot contains the page's interactive elements and their @e refs.
//
// Opt-in only (needs a real Chrome/Chromium on the machine):
//
//	go test -tags browserintegration ./mcp/ -run Real
func TestNavigateAndSnapshotAgainstRealChrome(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body><a href="/x">Home</a><input aria-label="Search box"><button>Go</button></body></html>`)
	}))
	defer srv.Close()

	b := newBrowserServer(types.BrowserConfig{AllowPrivateURLs: true, ProfileDir: t.TempDir() + "/p"})
	defer b.close()

	_, out, err := b.browserNavigate(context.Background(), nil, BrowserInput{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Snapshot, "Search box") || !strings.Contains(out.Snapshot, "@e") {
		t.Fatalf("snapshot missing the input/refs:\n%s", out.Snapshot)
	}
}
