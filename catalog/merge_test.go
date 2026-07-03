package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMerge_UnionDedupeAndPerSourceError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/a.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"skills":[{"name":"greeter","description":"da"},{"name":"shared","description":"x"}]}`))
	})
	mux.HandleFunc("/b.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"skills":[{"name":"shared","description":"y"}]}`))
	})
	// /c.json intentionally not registered → 404 → non-fatal SourceError.
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(srv)

	sources := []Source{
		{Label: "A", Kind: SourceIndex, URL: srv.URL + "/a.json", Trust: "official", Enabled: true},
		{Label: "B", Kind: SourceIndex, URL: srv.URL + "/b.json", Trust: "community", Enabled: true},
		{Label: "C", Kind: SourceIndex, URL: srv.URL + "/c.json", Trust: "community", Enabled: true},
		{Label: "D", Kind: SourceIndex, URL: srv.URL + "/a.json", Trust: "community", Enabled: false}, // disabled, ignored
	}
	metas, errs := c.Merge(context.Background(), sources)

	// A contributes greeter+shared; B contributes shared (different Source → kept).
	if len(metas) != 3 {
		t.Fatalf("want 3 metas, got %d: %+v", len(metas), metas)
	}
	// deterministic order (category "" then name): greeter, shared(A), shared(B).
	if metas[0].Name != "greeter" || metas[0].Source != "A" || metas[0].Trust != "official" {
		t.Fatalf("meta[0] provenance wrong: %+v", metas[0])
	}
	// one SourceError, from C.
	if len(errs) != 1 || errs[0].Source != "C" {
		t.Fatalf("want 1 SourceError from C, got %+v", errs)
	}
}

func TestMerge_DedupeSameSource(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/dup.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"skills":[{"name":"x","description":"1"},{"name":"x","description":"2"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(srv)
	metas, _ := c.Merge(context.Background(), []Source{{Label: "S", Kind: SourceIndex, URL: srv.URL + "/dup.json", Enabled: true}})
	if len(metas) != 1 {
		t.Fatalf("same (kind,name,source) must dedupe to 1, got %d", len(metas))
	}
}
