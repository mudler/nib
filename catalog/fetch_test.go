package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestClient returns a Client whose HTTP + GitHub base URLs point at srv.
func newTestClient(srv *httptest.Server) *Client {
	c := NewClient()
	c.HTTP = srv.Client()
	c.RawBase = srv.URL + "/raw"
	c.APIBase = srv.URL + "/api"
	return c
}

func TestFetch_IndexAndWellKnown(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/catalog/index.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"skills":[{"name":"a","description":"da"}]}`))
	})
	mux.HandleFunc("/.well-known/skills/index.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"skills":[{"name":"b","description":"db"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(srv)

	got, err := c.Fetch(context.Background(), Source{Kind: SourceIndex, URL: srv.URL + "/catalog/index.json"})
	if err != nil || len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("index fetch: %+v err=%v", got, err)
	}

	got, err = c.Fetch(context.Background(), Source{Kind: SourceWellKnown, URL: srv.URL})
	if err != nil || len(got) != 1 || got[0].Name != "b" {
		t.Fatalf("wellknown fetch: %+v err=%v", got, err)
	}
}

func TestFetch_GitHubIndexFirst(t *testing.T) {
	mux := http.NewServeMux()
	// index-first hit on the main branch, repo-root index.json.
	mux.HandleFunc("/raw/o/r/main/index.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"skills":[{"name":"gh","description":"from repo"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(srv)

	got, err := c.Fetch(context.Background(), Source{Kind: SourceGitHub, URL: "https://github.com/o/r"})
	if err != nil || len(got) != 1 || got[0].Name != "gh" {
		t.Fatalf("github index-first: %+v err=%v", got, err)
	}
}

func TestFetch_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusNotFound)
	}))
	defer srv.Close()
	c := newTestClient(srv)
	if _, err := c.Fetch(context.Background(), Source{Kind: SourceIndex, URL: srv.URL + "/missing.json"}); err == nil {
		t.Fatal("expected error on 404 index")
	}
}
