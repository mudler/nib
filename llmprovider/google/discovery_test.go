package google

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
			w.Write([]byte(`{"models":[{"name":"models/gemini-future"},{"name":"models/gemini-future"}],"nextPageToken":"next"}`))
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

func TestKnownChatModelsRefresh(t *testing.T) {
	ids := KnownChatModels()
	found := false
	for _, id := range ids {
		if id == "gemini-3.1-pro-preview" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing pinned chat hint: %v", ids)
	}
	ids[0] = "changed"
	if KnownChatModels()[0] == "changed" {
		t.Fatal("shared mutable hints")
	}
}
