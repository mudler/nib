package endpoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

func fingerprintConfig() types.Config {
	return types.Config{
		Provider: "openai", Model: "qwen3-coder", BaseURL: "http://localhost:8080/v1",
		APIKey: "sk-inline-secret", APIKeyEnv: "NIB_FP_KEY",
		Endpoints: types.Endpoints{
			{Name: "work", ModelProviderConfig: types.ModelProviderConfig{BaseURL: "https://vllm.corp/v1", Model: "llama", APIKeyEnv: "WORK_KEY"}},
			{Name: "other", ModelProviderConfig: types.ModelProviderConfig{BaseURL: "https://other.corp/v1", Model: "mistral"}},
		},
	}
}

func TestFingerprintIsStable(t *testing.T) {
	for _, sv := range []Saved{{ID: "regolo"}, {ID: "@work", Model: "llama-70b"}, {ID: DefaultID, Model: "x"}} {
		a := Fingerprint(fingerprintConfig(), sv)
		b := Fingerprint(fingerprintConfig(), sv)
		if a == "" || a != b {
			t.Fatalf("Fingerprint(%+v) = %q then %q, want one stable non-empty value", sv, a, b)
		}
	}
}

func TestFingerprintFollowsTheDefaultBlock(t *testing.T) {
	sv := Saved{ID: "regolo", Model: "glm"}
	base := Fingerprint(fingerprintConfig(), sv)
	for name, edit := range map[string]func(*types.Config){
		"provider":    func(c *types.Config) { c.Provider = "anthropic" },
		"base_url":    func(c *types.Config) { c.BaseURL = "http://localhost:9090/v1" },
		"model":       func(c *types.Config) { c.Model = "other-model" },
		"api_key_env": func(c *types.Config) { c.APIKeyEnv = "OTHER_KEY" },
	} {
		cfg := fingerprintConfig()
		edit(&cfg)
		if Fingerprint(cfg, sv) == base {
			t.Errorf("changing the default %s did not change the fingerprint", name)
		}
	}
}

func TestFingerprintFollowsThePickedNamedEndpoint(t *testing.T) {
	sv := Saved{ID: "@work", Model: "llama-70b"}
	base := Fingerprint(fingerprintConfig(), sv)
	for name, edit := range map[string]func(*types.ModelProviderConfig){
		"provider":    func(c *types.ModelProviderConfig) { c.Provider = "anthropic" },
		"base_url":    func(c *types.ModelProviderConfig) { c.BaseURL = "https://vllm2.corp/v1" },
		"model":       func(c *types.ModelProviderConfig) { c.Model = "llama-3" },
		"api_key_env": func(c *types.ModelProviderConfig) { c.APIKeyEnv = "OTHER_KEY" },
	} {
		cfg := fingerprintConfig()
		edit(&cfg.Endpoints[0].ModelProviderConfig)
		if Fingerprint(cfg, sv) == base {
			t.Errorf("changing @work's %s did not change the fingerprint", name)
		}
	}
}

// A bare pick (no model) follows its entry's own model live, so an edit to
// that model is not a conflict with the pick.
func TestFingerprintOfABarePickIgnoresTheEntryModel(t *testing.T) {
	sv := Saved{ID: "@work"}
	cfg := fingerprintConfig()
	base := Fingerprint(cfg, sv)
	cfg.Endpoints[0].Model = "llama-3"
	if Fingerprint(cfg, sv) != base {
		t.Fatalf("a bare pick's fingerprint changed with the entry's model")
	}
	cfg.Endpoints[0].BaseURL = "https://vllm2.corp/v1"
	if Fingerprint(cfg, sv) == base {
		t.Fatalf("a bare pick's fingerprint must still follow the entry's base_url")
	}
}

func TestFingerprintIgnoresUnrelatedNamedEndpoints(t *testing.T) {
	for _, sv := range []Saved{{ID: "@work", Model: "m"}, {ID: "regolo", Model: "m"}, {ID: DefaultID, Model: "m"}} {
		base := Fingerprint(fingerprintConfig(), sv)
		cfg := fingerprintConfig()
		cfg.Endpoints[1].BaseURL = "https://moved.corp/v1"
		cfg.Endpoints[1].Model = "changed"
		cfg.Endpoints = append(cfg.Endpoints, types.Endpoint{Name: "new", ModelProviderConfig: types.ModelProviderConfig{BaseURL: "http://x/v1"}})
		if Fingerprint(cfg, sv) != base {
			t.Errorf("pick %+v: editing an unrelated named endpoint changed the fingerprint", sv)
		}
	}
}

func TestFingerprintNeverCoversTheKeyValue(t *testing.T) {
	for _, sv := range []Saved{{ID: "@work", Model: "m"}, {ID: "regolo", Model: "m"}} {
		base := Fingerprint(fingerprintConfig(), sv)
		cfg := fingerprintConfig()
		cfg.APIKey = "sk-rotated"
		cfg.Endpoints[0].APIKey = "sk-work-rotated"
		if Fingerprint(cfg, sv) != base {
			t.Errorf("pick %+v: the fingerprint changed with an API key value", sv)
		}
		if strings.Contains(base, "secret") {
			t.Errorf("fingerprint %q carries the key", base)
		}
	}
}

func TestReconcileMatchingFingerprintKeepsThePick(t *testing.T) {
	set := testSet(t, startupConfig(t))
	sv := Saved{ID: "@work", Model: "llama-70b"}
	sv.ConfigFingerprint = set.Fingerprint(sv)
	got, rewrite, note := set.Reconcile(sv)
	if got != sv || rewrite || note != "" {
		t.Fatalf("Reconcile = %+v rewrite=%v note=%q, want the pick unchanged", got, rewrite, note)
	}
}

func TestReconcileChangedConfigDropsThePickWithANote(t *testing.T) {
	sv := Saved{ID: "regolo", Model: "glm5.2"}
	sv.ConfigFingerprint = testSet(t, startupConfig(t)).Fingerprint(sv)

	edited := startupConfig(t)
	edited.BaseURL = "http://localhost:9090/v1"
	got, rewrite, note := testSet(t, edited).Reconcile(sv)
	if got != (Saved{}) || !rewrite {
		t.Fatalf("Reconcile = %+v rewrite=%v, want the pick dropped", got, rewrite)
	}
	for _, want := range []string{"config.yaml changed", "regolo", "glm5.2", "starting on config.yaml"} {
		if !strings.Contains(note, want) {
			t.Fatalf("note = %q, missing %q", note, want)
		}
	}
}

func TestReconcileLegacyPickIsBackfilledSilently(t *testing.T) {
	set := testSet(t, startupConfig(t))
	got, rewrite, note := set.Reconcile(Saved{ID: "@work", Model: "llama-70b"})
	want := Saved{ID: "@work", Model: "llama-70b"}
	want.ConfigFingerprint = set.Fingerprint(want)
	if got != want || !rewrite || note != "" {
		t.Fatalf("Reconcile = %+v rewrite=%v note=%q, want %+v backfilled with no note", got, rewrite, note, want)
	}
}

// A pick whose endpoint is gone is Startup's to report ("no longer in
// config.yaml"); Reconcile must not pre-empt it with a vaguer note.
func TestReconcileLeavesAnUnresolvablePickToStartup(t *testing.T) {
	set := testSet(t, startupConfig(t))
	sv := Saved{ID: "@gone", Model: "m", ConfigFingerprint: "stale"}
	got, rewrite, note := set.Reconcile(sv)
	if got != sv || rewrite || note != "" {
		t.Fatalf("Reconcile = %+v rewrite=%v note=%q, want it left alone", got, rewrite, note)
	}
}

func TestReconcileNothingSaved(t *testing.T) {
	got, rewrite, note := testSet(t, startupConfig(t)).Reconcile(Saved{})
	if got != (Saved{}) || rewrite || note != "" {
		t.Fatalf("Reconcile = %+v rewrite=%v note=%q", got, rewrite, note)
	}
}

func TestWriteSavedRecordsTheFingerprint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provider.json")
	if err := WriteSaved(path, Saved{ID: "@work", Model: "llama", ConfigFingerprint: "abc"}); err != nil {
		t.Fatal(err)
	}
	if sv := LoadSaved(path); sv.ConfigFingerprint != "abc" {
		t.Fatalf("Saved = %#v, want the fingerprint round-tripped", sv)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"config_fingerprint"`) {
		t.Fatalf("file = %s, want config_fingerprint", data)
	}
}

func TestClearSavedRemovesThePick(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provider.json")
	if err := WriteSaved(path, Saved{ID: "@work"}); err != nil {
		t.Fatal(err)
	}
	if err := ClearSaved(path); err != nil {
		t.Fatal(err)
	}
	if err := ClearSaved(path); err != nil {
		t.Fatalf("clearing twice: %v", err)
	}
	if sv := LoadSaved(path); sv != (Saved{}) {
		t.Fatalf("Saved = %#v, want none", sv)
	}
}

// A bare pick of the default endpoint overrides nothing, so a config edit
// under it needs neither a note nor a rewrite.
func TestReconcileBareDefaultPickIsLeftAlone(t *testing.T) {
	sv := Saved{ID: DefaultID, ConfigFingerprint: "stale"}
	got, rewrite, note := testSet(t, startupConfig(t)).Reconcile(sv)
	if got != sv || rewrite || note != "" {
		t.Fatalf("Reconcile = %+v rewrite=%v note=%q", got, rewrite, note)
	}
}
