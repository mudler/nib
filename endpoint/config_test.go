package endpoint

import (
	"path/filepath"
	"testing"

	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/types"
)

func TestConfigDefaultIsTheTopLevelBlock(t *testing.T) {
	cfg := types.Config{Model: "qwen3-coder", BaseURL: "http://localhost:8080/v1", APIKey: "k"}
	got, err := testSet(t, cfg).Config(DefaultID)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if got.Model != "qwen3-coder" || got.BaseURL != "http://localhost:8080/v1" || got.APIKey != "k" {
		t.Fatalf("Config = %#v", got)
	}
}

func TestConfigNamedEndpointDoesNotInheritAddressing(t *testing.T) {
	cfg := types.Config{
		Model: "default-model", BaseURL: "http://default/v1", APIKey: "default-key",
		ReasoningEffort: "high",
		Endpoints: types.Endpoints{{
			Name:                "work",
			ModelProviderConfig: types.ModelProviderConfig{BaseURL: "https://vllm.corp/v1", Model: "llama"},
		}},
	}
	got, err := testSet(t, cfg).Config("@work")
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if got.BaseURL != "https://vllm.corp/v1" || got.Model != "llama" {
		t.Fatalf("addressing leaked: %#v", got)
	}
	if got.APIKey != "" {
		t.Fatalf("APIKey = %q, want empty (addressing does not inherit)", got.APIKey)
	}
	if got.Provider != "openai" {
		t.Fatalf("Provider = %q, want the openai default", got.Provider)
	}
	if got.ReasoningEffort != "high" {
		t.Fatalf("ReasoningEffort = %q, want inherited \"high\"", got.ReasoningEffort)
	}
	if got.MaxTokens != 0 {
		t.Fatalf("MaxTokens = %d, want 0: there is no top-level key to inherit", got.MaxTokens)
	}
}

func TestConfigNamedEndpointResolvesKeyFromEnv(t *testing.T) {
	t.Setenv("VLLM_KEY_TEST", "secret")
	cfg := types.Config{Endpoints: types.Endpoints{{
		Name:                "work",
		ModelProviderConfig: types.ModelProviderConfig{BaseURL: "https://vllm.corp/v1", APIKeyEnv: "VLLM_KEY_TEST"},
	}}}
	got, err := testSet(t, cfg).Config("@work")
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if got.APIKey != "secret" {
		t.Fatalf("APIKey = %q, want \"secret\"", got.APIKey)
	}
}

func TestConfigProviderReusesConfigKeyForTheSameProvider(t *testing.T) {
	cfg := types.Config{Provider: "anthropic", APIKey: "cfg-key"}
	got, err := testSet(t, cfg).Config("anthropic")
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if got.APIKey != "cfg-key" {
		t.Fatalf("APIKey = %q, want the config key reused", got.APIKey)
	}
}

func TestConfigProviderIgnoresConfigKeyPointingElsewhere(t *testing.T) {
	cfg := types.Config{Provider: "anthropic", APIKey: "cfg-key", BaseURL: "http://proxy.invalid/v1"}
	got, err := testSet(t, cfg).Config("anthropic")
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if got.APIKey != "" || got.BaseURL != "" {
		t.Fatalf("Config = %#v, want no carry-over from a redirected base_url", got)
	}
}

func TestConfigStoredCredentialBaseURLWins(t *testing.T) {
	store := auth.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err := store.Save(auth.Credential{
		ProviderID: "anthropic", Kind: auth.CredentialAPIKey,
		APIKey: "stored-key", BaseURL: "https://gateway.internal/v1",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Config with anthropic provider and APIKey that would normally carry over,
	// but stored credential's BaseURL must win.
	cfg := types.Config{Provider: "anthropic", APIKey: "cfg-key"}
	s, _ := New(cfg, store)
	got, err := s.Config("anthropic")
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if got.BaseURL != "https://gateway.internal/v1" {
		t.Fatalf("BaseURL = %q, want stored credential to win", got.BaseURL)
	}
}

func TestConfigUnknownID(t *testing.T) {
	s := testSet(t, types.Config{})
	// Unknown named endpoint.
	if _, err := s.Config("@nope"); err == nil {
		t.Fatal("want an error for unknown named endpoint @nope")
	}
	// Unknown bare provider ID.
	if _, err := s.Config("no-such-provider"); err == nil {
		t.Fatal("want an error for unknown provider no-such-provider")
	}
}

func TestConfigNamedEndpointInheritsStrictTools(t *testing.T) {
	off := false
	cfg := types.Config{
		StrictTools: true,
		Endpoints: types.Endpoints{
			{Name: "inherits", ModelProviderConfig: types.ModelProviderConfig{BaseURL: "http://a/v1"}},
			{Name: "optsout", ModelProviderConfig: types.ModelProviderConfig{BaseURL: "http://b/v1", StrictTools: &off}},
		},
	}
	set := testSet(t, cfg)
	for id, want := range map[string]bool{DefaultID: true, "@inherits": true, "@optsout": false} {
		got, err := set.Config(id)
		if err != nil {
			t.Fatalf("Config(%s): %v", id, err)
		}
		if got.StrictToolsEnabled() != want {
			t.Errorf("Config(%s).StrictToolsEnabled() = %v, want %v", id, got.StrictToolsEnabled(), want)
		}
	}
}
