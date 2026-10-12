package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

func TestLoadAndStats(t *testing.T) {
	providers, models := Stats()
	if providers == 0 {
		t.Fatalf("expected nonempty providers, got %d", providers)
	}
	if models < providers {
		t.Fatalf("expected at least one model per provider, got %d", models)
	}
}

func TestLookupByModelID(t *testing.T) {
	// GLM 5.2 exists under aiand provider as "zai-org/glm-5.2"
	m, ok := Lookup("aiand", "zai-org/glm-5.2", "")
	if !ok {
		t.Fatal("expected to find zai-org/glm-5.2 in aiand provider")
	}
	if m.MaxTokens == nil || *m.MaxTokens != 131072 {
		t.Fatalf("expected maxTokens=131072, got %v", m.MaxTokens)
	}
}

func TestLookupBySuffixMatch(t *testing.T) {
	// "glm-5.2" should match "zai-org/glm-5.2" via suffix matching
	m, ok := Lookup("regolo", "glm-5.2", "https://api.regolo.ai/v1")
	if !ok {
		t.Fatal("expected suffix match for glm-5.2")
	}
	if m.MaxTokens == nil || *m.MaxTokens != 131072 {
		t.Fatalf("expected maxTokens=131072, got %v", m.MaxTokens)
	}
}

func TestLookupNotFound(t *testing.T) {
	_, ok := Lookup("nonexistent", "no-such-model", "")
	if ok {
		t.Fatal("expected lookup to fail for nonexistent model")
	}
}

func TestResolveUserExplicit(t *testing.T) {
	config := types.ModelProviderConfig{
		Provider:  "regolo",
		Model:     "glm-5.2",
		MaxTokens: 4096,
	}
	res := ResolveMaxTokens(context.Background(), config, "https://api.regolo.ai/v1", "key")
	if res.MaxTokens != 4096 {
		t.Fatalf("expected 4096 (user override), got %d", res.MaxTokens)
	}
	if res.Source != "user-config" {
		t.Fatalf("expected source 'user-config', got %s", res.Source)
	}
	if res.Omit {
		t.Fatal("user-explicit should not be omitted")
	}
}

func TestResolveViaDiscovery(t *testing.T) {
	// Mock a /models endpoint that returns max_output_tokens
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"data": []map[string]interface{}{
				{"id": "glm-5.2", "max_output_tokens": 96000},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	config := types.ModelProviderConfig{
		Provider: "regolo",
		Model:    "glm-5.2",
	}
	res := ResolveMaxTokens(context.Background(), config, ts.URL, "key")
	if res.MaxTokens != 96000 {
		t.Fatalf("expected 96000 (discovered), got %d", res.MaxTokens)
	}
	if res.Source != "api-discovery" {
		t.Fatalf("expected source 'api-discovery', got %s", res.Source)
	}
}

func TestResolveViaDiscoveryMaxCompletionTokens(t *testing.T) {
	// vLLM-style response with max_completion_tokens
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"data": []map[string]interface{}{
				{"id": "some-model", "max_completion_tokens": 32768, "context_length": 131072},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	config := types.ModelProviderConfig{
		Provider: "test",
		Model:    "some-model",
	}
	res := ResolveMaxTokens(context.Background(), config, ts.URL, "key")
	if res.MaxTokens != 32768 {
		t.Fatalf("expected 32768 (discovered), got %d", res.MaxTokens)
	}
	if res.Source != "api-discovery" {
		t.Fatalf("expected source 'api-discovery', got %s", res.Source)
	}
}

func TestResolveFallbackDefault(t *testing.T) {
	// No discovery (mock returns empty), no catalog match
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"data": []interface{}{}})
	}))
	defer ts.Close()

	config := types.ModelProviderConfig{
		Provider: "unknown-provider",
		Model:    "unknown-model",
	}
	res := ResolveMaxTokens(context.Background(), config, ts.URL, "key")
	if res.MaxTokens != FallbackMaxTokens {
		t.Fatalf("expected %d (fallback), got %d", FallbackMaxTokens, res.MaxTokens)
	}
	if res.Source != "default" {
		t.Fatalf("expected source 'default', got %s", res.Source)
	}
}

func TestResolveViaCatalog(t *testing.T) {
	// No discovery, but catalog has the model.
	// Use a model from the catalog with a known maxTokens.
	config := types.ModelProviderConfig{
		Provider: "aiand",
		Model:    "zai-org/glm-5.2",
	}
	// Empty discovery server (returns no data)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"data": []interface{}{}})
	}))
	defer ts.Close()

	res := ResolveMaxTokens(context.Background(), config, ts.URL, "key")
	if res.MaxTokens != 131072 {
		t.Fatalf("expected 131072 (catalog), got %d", res.MaxTokens)
	}
	if res.Source != "catalog" {
		t.Fatalf("expected source 'catalog', got %s", res.Source)
	}
}

func TestResolveOmitForOpenRouter(t *testing.T) {
	config := types.ModelProviderConfig{
		Provider: "openrouter",
		Model:    "some-model",
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"data": []interface{}{}})
	}))
	defer ts.Close()

	res := ResolveMaxTokens(context.Background(), config, "https://openrouter.ai/api/v1", "key")
	if !res.Omit {
		t.Fatal("expected Omit=true for OpenRouter host without explicit user value")
	}
}

func TestResolveUserExplicitOverridesOpenRouterOmit(t *testing.T) {
	config := types.ModelProviderConfig{
		Provider:  "openrouter",
		Model:     "some-model",
		MaxTokens: 8192,
	}
	res := ResolveMaxTokens(context.Background(), config, "https://openrouter.ai/api/v1", "key")
	if res.Omit {
		t.Fatal("user-explicit value should not be omitted even for OpenRouter")
	}
	if res.MaxTokens != 8192 {
		t.Fatalf("expected 8192, got %d", res.MaxTokens)
	}
}

func TestResolveClampToModelMax(t *testing.T) {
	// deepseek-flash on deepseek provider has clampOutputToModelMax=true.
	// User sets a value above the model max, should be clamped.
	config := types.ModelProviderConfig{
		Provider:  "deepseek",
		Model:     "deepseek-flash",
		MaxTokens: 999999, // way above model max
	}
	res := ResolveMaxTokens(context.Background(), config, "https://api.deepseek.com/v1", "key")
	if res.MaxTokens > 384000 {
		t.Fatalf("expected clamp to <=384000, got %d", res.MaxTokens)
	}
	if res.MaxTokens != 384000 {
		t.Fatalf("expected clamp to exactly 384000, got %d", res.MaxTokens)
	}
}

func TestDiscoverModelNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"id": "other-model"},
			},
		})
	}))
	defer ts.Close()

	info, err := DiscoverModel(context.Background(), ts.URL, "key", "my-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Fatal("expected nil for model not in listing")
	}
}

func TestDiscoverModelError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	info, err := DiscoverModel(context.Background(), ts.URL, "key", "my-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info != nil {
		t.Fatal("expected nil for 500 response")
	}
}

// Lookup returns the first acceptable entry of a byModelID bucket, so a bucket
// in map order gave the same model a different maxTokens from run to run.
// Buckets must list their providers in sorted order.
func TestIndicesAreInProviderOrder(t *testing.T) {
	if err := Load(); err != nil {
		t.Fatal(err)
	}
	for id, matches := range byModelID {
		if !slices.IsSortedFunc(matches, func(a, b Model) int { return strings.Compare(a.Provider, b.Provider) }) {
			t.Fatalf("byModelID[%q] is not in provider order", id)
		}
	}
	if !slices.IsSorted(modelIDs) || len(modelIDs) != len(byModelID) {
		t.Fatal("modelIDs is not the sorted key set of byModelID")
	}
}

// Every catalog entry is found by its own provider and ID, whatever their
// case. Catalog IDs keep their original case ("zai-org/GLM-5.2") and one
// provider can list IDs that differ only by case, so the exact entry must win
// over a case-insensitive one.
func TestLookupFindsEveryEntryByItsOwnID(t *testing.T) {
	if err := Load(); err != nil {
		t.Fatal(err)
	}
	misses := 0
	for provider, models := range catalogDB {
		for id, want := range models {
			got, ok := Lookup(provider, id, "")
			if !ok || got.Provider != want.Provider || got.ID != want.ID {
				if misses < 5 {
					gotID := "<none>"
					if ok {
						gotID = got.Provider + "/" + got.ID
					}
					t.Errorf("Lookup(%q, %q) = %s", provider, id, gotID)
				}
				misses++
			}
		}
	}
	if misses > 0 {
		t.Fatalf("%d entries not found by their own provider and ID", misses)
	}
}

// A user who types a model ID in a different case than the catalog still gets
// that model, from step 3 (model ID across providers), not a suffix guess.
func TestLookupModelIDIgnoresCase(t *testing.T) {
	upper, ok := Lookup("no-such-provider", "ZAI-ORG/GLM-5.2", "")
	if !ok {
		t.Fatal("no match for ZAI-ORG/GLM-5.2")
	}
	lower, _ := Lookup("no-such-provider", "zai-org/glm-5.2", "")
	if upper.Provider != lower.Provider || upper.ID != lower.ID {
		t.Fatalf("case changed the match: %s/%s vs %s/%s", upper.Provider, upper.ID, lower.Provider, lower.ID)
	}
}

// These values are upstream metadata, not a claim of inference support.
func TestPinnedProviderSpecificLimits(t *testing.T) {
	for _, tc := range []struct {
		provider, id    string
		context, output int
	}{
		{"openai", "gpt-6.1-sol", 1050000, 128000},
		{"openai-codex", "gpt-6.1-sol", 500000, 128000},
		{"openai", "gpt-6-sol", 1050000, 128000},
		{"openai-codex", "gpt-6-sol", 272000, 128000},
		{"github-copilot", "claude-opus-4.8", 200000, 64000},
	} {
		t.Run(tc.provider+"/"+tc.id, func(t *testing.T) {
			m, ok := Lookup(tc.provider, tc.id, "")
			if !ok || m.Provider != tc.provider || m.ID != tc.id {
				t.Fatalf("wrong provider or spelling: %+v", m)
			}
			if m.ContextWindow == nil || *m.ContextWindow != tc.context || m.MaxTokens == nil || *m.MaxTokens != tc.output {
				t.Fatalf("wrong limits: %+v", m)
			}
		})
	}
}

func TestEmbeddedProvenance(t *testing.T) {
	raw, err := decompress(compressedCatalog)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := os.ReadFile("provenance.json")
	if err != nil {
		t.Fatal(err)
	}
	var p struct{ Revision, SHA256 string }
	if err := json.Unmarshal(meta, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Revision) != 40 || p.SHA256 != fmt.Sprintf("%x", sha256.Sum256(raw)) {
		t.Fatal("embedded artifact differs from provenance")
	}
}
