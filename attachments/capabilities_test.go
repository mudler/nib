package attachments

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchCapabilities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"vlm","input_modalities":["text","image"]},
			{"id":"omni","input_modalities":["text","image","audio"]}]}`))
	}))
	defer srv.Close()

	got := FetchCapabilities(context.Background(), srv.URL, "", "omni")
	if !got.Accepts("audio") || !got.Accepts("image") {
		t.Fatalf("omni should accept audio+image, got %v", got.InputModalities)
	}
}

func TestFetchCapabilitiesFailSafe(t *testing.T) {
	// unreachable server URL → fail-safe to text-only
	got := FetchCapabilities(context.Background(), "http://127.0.0.1:0", "", "whatever")
	if got.Accepts("image") || !got.Accepts("text") {
		t.Fatalf("fail-safe must be text-only, got %v", got.InputModalities)
	}
}
