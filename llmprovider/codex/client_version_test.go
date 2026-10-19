package codex

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestSupportedClientVersionOnInferenceAndDiscovery(t *testing.T) {
	l := streamClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Older advertised clients can receive an unsupported-model rejection
		// even when the same request succeeds with this client version.
		if got := r.Header.Get("version"); got != "0.162.1" {
			t.Errorf("%s version header = %q, want 0.162.1", r.URL.Path, got)
		}
		switch r.URL.Path {
		case "/backend-api/codex/responses":
			fmt.Fprint(w, "data: [DONE]\n\n")
		case "/backend-api/codex/models":
			if got := r.URL.Query().Get("client_version"); got != "0.162.1" {
				t.Errorf("discovery client_version = %q, want 0.162.1", got)
			}
			fmt.Fprint(w, `{"models":[]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	resp, err := l.openResponse(context.Background(), openai.ChatCompletionRequest{Model: "gpt-6.1-sol"})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if _, err := l.fetchModels(context.Background()); err != nil {
		t.Fatal(err)
	}
}
