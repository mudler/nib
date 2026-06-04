package setup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer srv.Close()

	if err := Probe(context.Background(), "any-model", "sk-test", srv.URL+"/v1"); err != nil {
		t.Fatalf("Probe should succeed, got %v", err)
	}
}

func TestProbeFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	if err := Probe(context.Background(), "m", "k", srv.URL+"/v1"); err == nil {
		t.Fatal("Probe should return an error on 500")
	}
}
