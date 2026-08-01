package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mudler/nib/slash"
	"github.com/mudler/nib/types"
)

// modelsEndpoint serves an OpenAI-shaped /v1/models listing. Nothing in these
// tests starts a turn, so the chat-completions side is never exercised.
func modelsEndpoint(t *testing.T, ids ...string) types.Config {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := make([]map[string]string, 0, len(ids))
		for _, id := range ids {
			data = append(data, map[string]string{"id": id, "object": "model"})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
	}))
	t.Cleanup(srv.Close)
	return types.Config{Model: ids[0], BaseURL: srv.URL + "/v1"}
}

func TestResolveCLIInputModel(t *testing.T) {
	cfg := types.Config{}
	if a := resolveCLIInput("/models", cfg); a.Kind != slash.KindModelList {
		t.Fatalf("/models resolved to %+v", a)
	}
	if a := resolveCLIInput("/model", cfg); a.Kind != slash.KindModelList {
		t.Fatalf("bare /model should list, resolved to %+v", a)
	}
	if a := resolveCLIInput("/model foo", cfg); a.Kind != slash.KindModelSet || a.Model != "foo" {
		t.Fatalf("/model foo resolved to %+v", a)
	}
}

func TestCLIModelsListsWithTheCurrentMarked(t *testing.T) {
	out, errOut := runCLIScript(t, modelsEndpoint(t, "model-a", "model-b"), "/models\nexit\n")

	if !strings.Contains(out, "* model-a") {
		t.Fatalf("current model not marked in %q", out)
	}
	if !strings.Contains(out, "  model-b") {
		t.Fatalf("other model missing from %q", out)
	}
	if errOut != "" {
		t.Fatalf("nothing should have reached stderr, got %q", errOut)
	}
}

func TestCLIModelSwitchesTheSession(t *testing.T) {
	// The second /models proves the session really moved: the star follows.
	out, errOut := runCLIScript(t, modelsEndpoint(t, "model-a", "model-b"), "/model model-b\n/models\nexit\n")

	if !strings.Contains(out, "model: model-b") {
		t.Fatalf("switch not acknowledged in %q", out)
	}
	if !strings.Contains(out, "* model-b") {
		t.Fatalf("the session did not move to model-b: %q", out)
	}
	if errOut != "" {
		t.Fatalf("nothing should have reached stderr, got %q", errOut)
	}
}

// A typo must be refused at the prompt, naming what is available, instead of
// switching and 404ing on the next turn.
func TestCLIModelRejectsANameTheEndpointDoesNotServe(t *testing.T) {
	out, errOut := runCLIScript(t, modelsEndpoint(t, "model-a", "model-b"), "/model model-c\n/models\nexit\n")

	if !strings.Contains(errOut, "model-c") || !strings.Contains(errOut, "not served") {
		t.Fatalf("stderr should refuse the unknown name, got %q", errOut)
	}
	if strings.Contains(out, "model: model-c") {
		t.Fatalf("the switch should not have been acknowledged: %q", out)
	}
	if !strings.Contains(out, "* model-a") {
		t.Fatalf("the session should still be on model-a: %q", out)
	}
}

// /models against an endpoint that cannot be listed reports the failure on
// stderr rather than printing an empty listing.
func TestCLIModelsReportsAFailingEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"nope"}}`, http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	out, errOut := runCLIScript(t, types.Config{Model: "model-a", BaseURL: srv.URL + "/v1"}, "/models\nexit\n")

	if !strings.Contains(errOut, "500") {
		t.Fatalf("stderr should carry the endpoint's failure, got %q (stdout %q)", errOut, out)
	}
	if strings.Contains(out, "no models available") {
		t.Fatalf("a failure must not be rendered as an empty listing: %q", out)
	}
}
