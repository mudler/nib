package googlevertex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscoveryPreservesPartial(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Write([]byte(`{"publisherModels":[{"name":"publishers/google/models/gemini-future"},{"name":"publishers/google/models/gemini-future"}],"nextPageToken":"next"}`))
			return
		}
		http.Error(w, "later failure", 500)
	}))
	defer srv.Close()
	ids, err := New(Config{BaseURL: srv.URL, IsOAuth: true, Token: "test"}).ListModels(context.Background())
	if err == nil || len(ids) != 1 || ids[0] != "gemini-future" {
		t.Fatalf("ids=%v err=%v; want deduplicated partial IDs and diagnostic", ids, err)
	}
}
