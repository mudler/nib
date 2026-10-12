package googlevertex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestListModelsGeminiOnly(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1beta1/publishers/google/models" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer ya29" || r.Header.Get("x-goog-user-project") != "proj" {
			t.Errorf("oauth headers = %v", r.Header)
		}
		if r.URL.Query().Get("pageToken") == "" {
			_, _ = w.Write([]byte(`{"publisherModels":[
				{"name":"publishers/google/models/gemini-2.5-pro"},
				{"name":"publishers/google/models/imagen-4.0-generate-001"},
				{"name":"publishers/google/models/gemini-embedding-001"}
			],"nextPageToken":"n"}`))
			return
		}
		_, _ = w.Write([]byte(`{"publisherModels":[{"name":"publishers/google/models/gemini-2.5-flash"}]}`))
	}))
	defer srv.Close()

	ids, err := New(Config{BaseURL: srv.URL, Token: "ya29", IsOAuth: true, Project: "proj"}).ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"gemini-2.5-pro", "gemini-2.5-flash"}; !reflect.DeepEqual(ids, want) || calls != 2 {
		t.Fatalf("ids = %v after %d calls, want %v", ids, calls, want)
	}
}

func TestListModelsCustomAPIKeyHasNoVendorHints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("API-key mode must not call the listing, which refuses API keys")
	}))
	defer srv.Close()
	ids, err := New(Config{BaseURL: srv.URL, APIKey: "AQ.key"}).ListModels(context.Background())
	if err == nil || len(ids) != 0 {
		t.Fatalf("ids = %v, err = %v", ids, err)
	}
}

func TestListModelsOfficialAPIKeyOffersKnownModels(t *testing.T) {
	result := New(Config{APIKey: "fixture"}).DiscoverModels(context.Background())
	if result.Err != nil || !result.Partial || len(result.IDs) == 0 {
		t.Fatalf("result=%+v", result)
	}
}
