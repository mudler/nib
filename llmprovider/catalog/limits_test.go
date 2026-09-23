package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/mudler/nib/types"
)

// limitsServer serves the three discovery endpoints from canned bodies. A nil
// body answers 404, which is what a provider without that endpoint returns.
// It records the paths it was asked for so a test can check that discovery
// stops once it knows both limits.
type limitsServer struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
}

func newLimitsServer(t *testing.T, capabilities, models, modelInfo any) *limitsServer {
	t.Helper()
	ls := &limitsServer{}
	bodies := map[string]any{
		"/v1/models/capabilities": capabilities,
		"/v1/models":              models,
		"/v1/model/info":          modelInfo,
	}
	ls.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ls.mu.Lock()
		ls.paths = append(ls.paths, r.URL.Path)
		ls.mu.Unlock()
		body, ok := bodies[r.URL.Path]
		if !ok || body == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(ls.Close)
	return ls
}

func (ls *limitsServer) base() string { return ls.URL + "/v1" }

func (ls *limitsServer) asked(path string) bool {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return slices.Contains(ls.paths, path)
}

// regoloModelInfo is the shape LiteLLM returned for regolo's glm5.2: /models
// carries no limits at all, /model/info does.
var regoloModelInfo = map[string]any{
	"data": []map[string]any{
		{"model_name": "other", "model_info": map[string]any{"max_input_tokens": 1000, "max_output_tokens": 100}},
		{"model_name": "glm5.2", "model_info": map[string]any{
			"max_input_tokens":  96000,
			"max_tokens":        200000,
			"max_output_tokens": 96000,
		}},
	},
}

var bareModels = map[string]any{
	"object": "list",
	"data": []map[string]any{
		{"id": "glm5.2", "object": "model", "created": 1, "owned_by": "openai"},
	},
}

func TestDiscoverLimitsLiteLLMOnly(t *testing.T) {
	ls := newLimitsServer(t, nil, bareModels, regoloModelInfo)

	got := DiscoverLimits(context.Background(), ls.base(), "key", "glm5.2")
	if got.ContextWindow != 96000 || got.ContextSource != SourceLiteLLMModelInfo {
		t.Fatalf("window = %d from %q, want 96000 from %q", got.ContextWindow, got.ContextSource, SourceLiteLLMModelInfo)
	}
	if got.OutputCap != 96000 || got.OutputSource != SourceLiteLLMModelInfo {
		t.Fatalf("cap = %d from %q, want 96000 from %q", got.OutputCap, got.OutputSource, SourceLiteLLMModelInfo)
	}
}

func TestDiscoverLimitsLiteLLMMaxTokensIsNotAWindow(t *testing.T) {
	// LiteLLM's max_tokens means the input window for some entries and the
	// output cap for others, so it must not be read as either.
	info := map[string]any{"data": []map[string]any{
		{"model_name": "m", "model_info": map[string]any{"max_tokens": 200000}},
	}}
	ls := newLimitsServer(t, nil, nil, info)

	got := DiscoverLimits(context.Background(), ls.base(), "", "m")
	if got.ContextWindow != 0 || got.OutputCap != 0 {
		t.Fatalf("got window %d cap %d, want both 0", got.ContextWindow, got.OutputCap)
	}
}

func TestDiscoverLimitsLocalAICapabilitiesWins(t *testing.T) {
	caps := map[string]any{"data": []map[string]any{{"id": "glm5.2", "context_size": 32768}}}
	models := map[string]any{"data": []map[string]any{{"id": "glm5.2", "context_length": 131072}}}
	ls := newLimitsServer(t, caps, models, regoloModelInfo)

	got := DiscoverLimits(context.Background(), ls.base(), "", "glm5.2")
	if got.ContextWindow != 32768 || got.ContextSource != SourceLocalAICapabilities {
		t.Fatalf("window = %d from %q, want 32768 from %q", got.ContextWindow, got.ContextSource, SourceLocalAICapabilities)
	}
	// Capabilities carries no output cap; the next source that knows one does.
	if got.OutputCap != 96000 || got.OutputSource != SourceLiteLLMModelInfo {
		t.Fatalf("cap = %d from %q, want 96000 from %q", got.OutputCap, got.OutputSource, SourceLiteLLMModelInfo)
	}
}

func TestDiscoverLimitsModelsContextLength(t *testing.T) {
	models := map[string]any{"data": []map[string]any{
		{"id": "some-model", "context_length": 131072, "max_completion_tokens": 32768},
	}}
	ls := newLimitsServer(t, nil, models, regoloModelInfo)

	got := DiscoverLimits(context.Background(), ls.base(), "", "some-model")
	if got.ContextWindow != 131072 || got.ContextSource != SourceModels {
		t.Fatalf("window = %d from %q, want 131072 from %q", got.ContextWindow, got.ContextSource, SourceModels)
	}
	if got.OutputCap != 32768 || got.OutputSource != SourceModels {
		t.Fatalf("cap = %d from %q, want 32768 from %q", got.OutputCap, got.OutputSource, SourceModels)
	}
	if ls.asked("/v1/model/info") {
		t.Fatal("asked /model/info after /models already answered both limits")
	}
}

func TestDiscoverLimitsModelsVLLMMaxModelLen(t *testing.T) {
	models := map[string]any{"data": []map[string]any{{"id": "qwen", "max_model_len": 40960}}}
	ls := newLimitsServer(t, nil, models, nil)

	got := DiscoverLimits(context.Background(), ls.base(), "", "qwen")
	if got.ContextWindow != 40960 || got.ContextSource != SourceModels {
		t.Fatalf("window = %d from %q, want 40960 from %q", got.ContextWindow, got.ContextSource, SourceModels)
	}
}

func TestDiscoverLimitsPartialAnswers(t *testing.T) {
	// /models knows only the cap, /model/info only the window.
	models := map[string]any{"data": []map[string]any{{"id": "m", "max_completion_tokens": 8192}}}
	info := map[string]any{"data": []map[string]any{
		{"model_name": "m", "model_info": map[string]any{"max_input_tokens": 64000, "max_output_tokens": 4096}},
	}}
	ls := newLimitsServer(t, nil, models, info)

	got := DiscoverLimits(context.Background(), ls.base(), "", "m")
	if got.ContextWindow != 64000 || got.ContextSource != SourceLiteLLMModelInfo {
		t.Fatalf("window = %d from %q, want 64000 from %q", got.ContextWindow, got.ContextSource, SourceLiteLLMModelInfo)
	}
	if got.OutputCap != 8192 || got.OutputSource != SourceModels {
		t.Fatalf("cap = %d from %q, want 8192 from %q (first source wins)", got.OutputCap, got.OutputSource, SourceModels)
	}
}

func TestDiscoverLimitsModelAbsentEverywhere(t *testing.T) {
	caps := map[string]any{"data": []map[string]any{{"id": "x", "context_size": 1}}}
	models := map[string]any{"data": []map[string]any{{"id": "x", "context_length": 2}}}
	ls := newLimitsServer(t, caps, models, regoloModelInfo)

	got := DiscoverLimits(context.Background(), ls.base(), "", "missing")
	if got != (Limits{}) {
		t.Fatalf("got %+v, want zero Limits", got)
	}
	for _, p := range []string{"/v1/models/capabilities", "/v1/models", "/v1/model/info"} {
		if !ls.asked(p) {
			t.Fatalf("did not ask %s before giving up", p)
		}
	}
}

func TestDiscoverLimitsCanceledContextSendsNothing(t *testing.T) {
	ls := newLimitsServer(t, nil, bareModels, regoloModelInfo)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got := DiscoverLimits(ctx, ls.base(), "", "glm5.2")
	if got != (Limits{}) {
		t.Fatalf("got %+v, want zero Limits", got)
	}
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if len(ls.paths) != 0 {
		t.Fatalf("sent %v on a canceled context", ls.paths)
	}
}

func TestResolveViaLiteLLMModelInfo(t *testing.T) {
	ls := newLimitsServer(t, nil, bareModels, regoloModelInfo)

	config := types.ModelProviderConfig{Provider: "test", Model: "glm5.2"}
	res := ResolveMaxTokens(context.Background(), config, ls.base(), "key")
	if res.MaxTokens != 96000 {
		t.Fatalf("expected 96000 (LiteLLM /model/info), got %d from %s", res.MaxTokens, res.Source)
	}
	if res.Source != "api-discovery:"+SourceLiteLLMModelInfo {
		t.Fatalf("expected source %q, got %s", "api-discovery:"+SourceLiteLLMModelInfo, res.Source)
	}
	// The output cap needs no window, so the LocalAI window probe is skipped.
	if ls.asked("/v1/models/capabilities") {
		t.Fatal("ResolveMaxTokens asked /models/capabilities, which carries no output cap")
	}
}

// capabilitiesWindow runs the LocalAI capabilities source alone, as the
// window probe chat/ used to own did.
func capabilitiesWindow(t *testing.T, baseURL, apiKey, model string) int {
	t.Helper()
	window, _, _ := fetchLocalAICapabilities(context.Background(), baseURL, apiKey, model)
	return window
}

func TestLocalAICapabilitiesWindow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models/capabilities" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "model-a", "context_size": 32768},
				{"id": "model-b", "context_size": 8192},
			},
		})
	}))
	defer srv.Close()

	got := capabilitiesWindow(t, srv.URL, "", "model-a")
	if got != 32768 {
		t.Fatalf("capabilitiesWindow(model-a) = %d, want 32768", got)
	}
	got = capabilitiesWindow(t, srv.URL, "", "model-b")
	if got != 8192 {
		t.Fatalf("capabilitiesWindow(model-b) = %d, want 8192", got)
	}
	// Model not in the list
	got = capabilitiesWindow(t, srv.URL, "", "model-c")
	if got != 0 {
		t.Fatalf("capabilitiesWindow(model-c) = %d, want 0", got)
	}
}

func TestLocalAICapabilitiesWindowMissingField(t *testing.T) {
	// A model entry without context_size should yield 0, not a panic.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "no-ctx"},
			},
		})
	}))
	defer srv.Close()

	got := capabilitiesWindow(t, srv.URL, "", "no-ctx")
	if got != 0 {
		t.Fatalf("capabilitiesWindow(no-ctx) = %d, want 0", got)
	}
}

func TestLocalAICapabilitiesWindowServerDown(t *testing.T) {
	// A non-responsive server returns 0 (best-effort).
	got := capabilitiesWindow(t, "http://127.0.0.1:1", "", "any")
	if got != 0 {
		t.Fatalf("capabilitiesWindow(unreachable) = %d, want 0", got)
	}
}

func TestLocalAICapabilitiesWindowNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	got := capabilitiesWindow(t, srv.URL, "", "any")
	if got != 0 {
		t.Fatalf("capabilitiesWindow(404) = %d, want 0", got)
	}
}
