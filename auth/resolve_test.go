package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mudler/nib/provider"
)

func testDef(id string) provider.Definition {
	return provider.Definition{
		ID:     id,
		EnvVar: "TEST_KEY_" + id,
	}
}

func TestResolveStoredAPIKey(t *testing.T) {
	s := NewStore(testStorePath(t))
	_ = s.Save(Credential{ProviderID: "test", Kind: CredentialAPIKey, APIKey: "stored-key"})

	def := testDef("test")
	r, err := Resolve(s, def, "config-key")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.APIKey != "stored-key" {
		t.Errorf("APIKey = %q, want 'stored-key'", r.APIKey)
	}
	if r.IsOAuth {
		t.Error("IsOAuth should be false for API key")
	}
}

func TestResolveStoredOAuthValid(t *testing.T) {
	s := NewStore(testStorePath(t))
	_ = s.Save(Credential{
		ProviderID:   "test",
		Kind:         CredentialOAuth,
		AccessToken:  "valid-tok",
		RefreshToken: "ref",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	})

	def := testDef("test")
	r, err := Resolve(s, def, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.APIKey != "valid-tok" {
		t.Errorf("APIKey = %q, want 'valid-tok'", r.APIKey)
	}
	if !r.IsOAuth {
		t.Error("IsOAuth should be true")
	}
}

func TestResolveStoredOAuthRefreshSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "refreshed-tok",
			"refresh_token": "new-ref",
			"expires_in":    3600,
			"token_type":    "Bearer",
		})
	}))
	defer server.Close()

	s := NewStore(testStorePath(t))
	_ = s.Save(Credential{
		ProviderID:   "test",
		Kind:         CredentialOAuth,
		AccessToken:  "old-tok",
		RefreshToken: "old-ref",
		ExpiresAt:    time.Now().Add(-1 * time.Minute), // expired
	})

	def := testDef("test")
	def.TokenURL = server.URL + "/token"
	def.ClientID = "test-client"

	r, err := Resolve(s, def, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.APIKey != "refreshed-tok" {
		t.Errorf("APIKey = %q, want 'refreshed-tok'", r.APIKey)
	}
	if !r.IsOAuth {
		t.Error("IsOAuth should be true")
	}

	// Verify refreshed credential was persisted.
	got, _, _ := s.Get("test")
	if got.AccessToken != "refreshed-tok" {
		t.Errorf("stored AccessToken = %q, want 'refreshed-tok'", got.AccessToken)
	}
	if got.RefreshToken != "new-ref" {
		t.Errorf("stored RefreshToken = %q, want 'new-ref'", got.RefreshToken)
	}
}

func TestResolveStoredOAuthRefreshFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid_grant", http.StatusBadRequest)
	}))
	defer server.Close()

	s := NewStore(testStorePath(t))
	_ = s.Save(Credential{
		ProviderID:   "test",
		Kind:         CredentialOAuth,
		AccessToken:  "old-tok",
		RefreshToken: "old-ref",
		ExpiresAt:    time.Now().Add(-1 * time.Minute), // expired
	})

	def := testDef("test")
	def.TokenURL = server.URL + "/token"
	def.ClientID = "test-client"

	r, err := Resolve(s, def, "fallback-key")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// On refresh failure, resolveOAuth returns empty and Resolve falls
	// through to configAPIKey.
	if r.APIKey != "fallback-key" {
		t.Errorf("APIKey = %q, want 'fallback-key' (should fall through on refresh failure)", r.APIKey)
	}
}

func TestResolveConfigAPIKey(t *testing.T) {
	s := NewStore(testStorePath(t)) // empty store
	def := testDef("test")
	r, err := Resolve(s, def, "config-api-key")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.APIKey != "config-api-key" {
		t.Errorf("APIKey = %q, want 'config-api-key'", r.APIKey)
	}
}

func TestResolveEnvVar(t *testing.T) {
	s := NewStore(testStorePath(t)) // empty store
	def := testDef("TESTPROVIDER")
	t.Setenv("TEST_KEY_TESTPROVIDER", "env-key")

	r, err := Resolve(s, def, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.APIKey != "env-key" {
		t.Errorf("APIKey = %q, want 'env-key'", r.APIKey)
	}
}

func TestResolveStoredOAuthRefreshFailureEnvVar(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid_grant", http.StatusBadRequest)
	}))
	defer server.Close()

	s := NewStore(testStorePath(t))
	_ = s.Save(Credential{
		ProviderID:   "TESTPROVIDER_ENV_FALL",
		Kind:         CredentialOAuth,
		AccessToken:  "old-tok",
		RefreshToken: "old-ref",
		ExpiresAt:    time.Now().Add(-1 * time.Minute),
	})

	def := testDef("TESTPROVIDER_ENV_FALL")
	def.TokenURL = server.URL + "/token"
	def.ClientID = "test-client"
	t.Setenv("TEST_KEY_TESTPROVIDER_ENV_FALL", "env-fallback-key")

	r, err := Resolve(s, def, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.APIKey != "env-fallback-key" {
		t.Errorf("APIKey = %q, want 'env-fallback-key' (should fall through to env on refresh failure)", r.APIKey)
	}
}

func TestResolveFallbackEmpty(t *testing.T) {
	s := NewStore(testStorePath(t)) // empty store
	def := testDef("TESTPROVIDER2") // no env var set
	r, err := Resolve(s, def, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.APIKey != "" {
		t.Errorf("APIKey = %q, want empty", r.APIKey)
	}
}

func TestResolveStoreTakesPriorityOverConfig(t *testing.T) {
	s := NewStore(testStorePath(t))
	_ = s.Save(Credential{ProviderID: "test", Kind: CredentialAPIKey, APIKey: "stored"})

	def := testDef("test")
	r, _ := Resolve(s, def, "config")
	if r.APIKey != "stored" {
		t.Errorf("APIKey = %q, want 'stored' (store should win)", r.APIKey)
	}
}

func TestResolveNilStore(t *testing.T) {
	def := testDef("TESTPROVIDER3")
	r, err := Resolve(nil, def, "config-key")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.APIKey != "config-key" {
		t.Errorf("APIKey = %q, want 'config-key'", r.APIKey)
	}
}

func TestResolveOrErrorNoCredentials(t *testing.T) {
	s := NewStore(testStorePath(t)) // empty store
	def := testDef("TESTPROVIDER4") // no env var
	_, err := ResolveOrError(s, def, "")
	if err == nil {
		t.Fatal("expected error when no credentials found")
	}
}

func TestResolveOrErrorWithConfigKey(t *testing.T) {
	s := NewStore(testStorePath(t))
	def := testDef("test")
	r, err := ResolveOrError(s, def, "config-key")
	if err != nil {
		t.Fatalf("ResolveOrError: %v", err)
	}
	if r.APIKey != "config-key" {
		t.Errorf("APIKey = %q, want 'config-key'", r.APIKey)
	}
}

func TestResolveOrErrorWithStoredCred(t *testing.T) {
	s := NewStore(testStorePath(t))
	_ = s.Save(Credential{ProviderID: "test", Kind: CredentialAPIKey, APIKey: "stored"})
	def := testDef("test")
	r, err := ResolveOrError(s, def, "")
	if err != nil {
		t.Fatalf("ResolveOrError: %v", err)
	}
	if r.APIKey != "stored" {
		t.Errorf("APIKey = %q, want 'stored'", r.APIKey)
	}
}

func TestAnthropicAPIKeyResolutionIsolation(t *testing.T) {
	// A definition literal also exercises the guard before registry support exists.
	def := provider.Definition{ID: "anthropic-api-key", EnvVar: "ANTHROPIC_API_KEY"}
	for _, tc := range []struct{ name, stored, config, env, want string }{
		{"stored", "sentinel-stored", "sentinel-config", "sentinel-env", "sentinel-stored"},
		{"config", "", "sentinel-config", "sentinel-env", "sentinel-config"},
		{"env", "", "", "sentinel-env", "sentinel-env"},
		{"missing", "", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(def.EnvVar, tc.env)
			s := NewStore(testStorePath(t))
			if err := s.Save(Credential{ProviderID: "anthropic", Kind: CredentialOAuth, AccessToken: "sentinel-oauth", RefreshToken: "sentinel-refresh", ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
				t.Fatal(err)
			}
			before, _, _ := s.Get("anthropic")
			if err := s.Save(Credential{ProviderID: def.ID, Kind: CredentialAPIKey, APIKey: tc.stored}); err != nil {
				t.Fatal(err)
			}
			got, err := Resolve(s, def, tc.config)
			if err != nil || got.APIKey != tc.want || got.IsOAuth {
				t.Fatalf("unexpected resolution: %+v, %v", got, err)
			}
			after, _, _ := s.Get("anthropic")
			if !reflect.DeepEqual(before, after) {
				t.Fatal("OAuth credential changed")
			}
		})
	}
	for _, kind := range []CredentialKind{CredentialOAuth, "unknown", ""} {
		t.Run("incompatible-"+string(kind), func(t *testing.T) {
			t.Setenv(def.EnvVar, "sentinel-env")
			s := NewStore(testStorePath(t))
			if err := s.Save(Credential{ProviderID: def.ID, Kind: kind, AccessToken: "sentinel-token", RefreshToken: "sentinel-refresh", ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
				t.Fatal(err)
			}
			before, _, _ := s.Get(def.ID)
			_, err := Resolve(s, def, "sentinel-config")
			if err == nil || !strings.Contains(err.Error(), "nib login anthropic-api-key") {
				t.Fatalf("want actionable incompatible-credential error, got %v", err)
			}
			after, _, _ := s.Get(def.ID)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("incompatible credential refreshed or modified")
			}
		})
	}
}
