package anthropic

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
			w.Write([]byte(`{"data":[{"id":"gemini-future"},{"id":"gemini-future"}],"has_more":true,"last_id":"next"}`))
			return
		}
		http.Error(w, "later failure", 500)
	}))
	defer srv.Close()
	ids, err := New(Config{BaseURL: srv.URL, APIKey: "test"}).ListModels(context.Background())
	if err == nil || len(ids) != 1 || ids[0] != "gemini-future" {
		t.Fatalf("ids=%v err=%v; want deduplicated partial IDs and diagnostic", ids, err)
	}
}
