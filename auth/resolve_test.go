package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
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
	def, _ := provider.Get("anthropic-api-key")
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
}

func TestResolveAPIKeyOnlyCompatibility(t *testing.T) {
	for _, id := range []string{"anthropic-api-key", "groq"} {
		def, ok := provider.Get(id)
		if !ok || def.LoginKind != provider.LoginAPIKey {
			t.Fatalf("expected API-key definition: %s", id)
		}
		for _, kind := range []CredentialKind{CredentialOAuth, "unknown", "", CredentialAPIKey} {
			for _, source := range []string{"config", "env", "missing"} {
				t.Run(id+"/"+string(kind)+"/"+source, func(t *testing.T) {
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(`{"access_token":"refreshed-sentinel","refresh_token":"changed","expires_in":3600}`))
					}))
					defer server.Close()
					def.TokenURL = server.URL
					s := NewStore(testStorePath(t))
					cred := Credential{ProviderID: id, Kind: kind, APIKey: "stored-sentinel", AccessToken: "oauth-sentinel", RefreshToken: "refresh-sentinel", ExpiresAt: time.Now().Add(-time.Hour)}
					if err := s.Save(cred); err != nil {
						t.Fatal(err)
					}
					before, err := os.ReadFile(s.Path)
					if err != nil {
						t.Fatal(err)
					}
					config, env, want := "", "", ""
					if source == "config" {
						config, env, want = "config-sentinel", "env-sentinel", "config-sentinel"
					}
					if source == "env" {
						env, want = "env-sentinel", "env-sentinel"
					}
					if kind == CredentialAPIKey {
						want = cred.APIKey
					}
					t.Setenv(def.EnvVar, env)
					got, err := Resolve(s, def, config)
					if err != nil || got != (Resolved{APIKey: want}) {
						t.Errorf("Resolve = %+v, %v; want key %q", got, err, want)
					}
					required, err := ResolveOrError(s, def, config)
					if want == "" {
						if err == nil || !strings.Contains(err.Error(), "no credentials for "+id) || !strings.Contains(err.Error(), "nib login "+id) || !strings.Contains(err.Error(), def.EnvVar) {
							t.Errorf("want normal actionable missing error, got %v", err)
						}
					} else if err != nil || required != got {
						t.Errorf("ResolveOrError = %+v, %v", required, err)
					}
					if calls.Load() != 0 {
						t.Errorf("incompatible credential triggered %d refreshes", calls.Load())
					}
					after, err := os.ReadFile(s.Path)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(before, after) {
						t.Error("stored credential changed")
					}
				})
			}
		}
	}
}

func TestResolveOtherLoginKindsPreserveCredentials(t *testing.T) {
	for _, login := range []provider.LoginKind{provider.LoginNone, provider.LoginOAuthCode, provider.LoginDeviceCode, provider.LoginCopilot, "future-kind"} {
		for _, kind := range []CredentialKind{CredentialOAuth, CredentialAPIKey} {
			t.Run(string(login)+"/"+string(kind), func(t *testing.T) {
				def, _ := provider.Get("anthropic")
				def.LoginKind = login
				s := NewStore(testStorePath(t))
				if err := s.Save(Credential{ProviderID: def.ID, Kind: kind, APIKey: "stored-key", AccessToken: "stored-oauth", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
				t.Setenv(def.EnvVar, "env-key")
				got, err := Resolve(s, def, "config-key")
				want := Resolved{APIKey: "stored-key"}
				if kind == CredentialOAuth {
					want = Resolved{APIKey: "stored-oauth", IsOAuth: true}
				}
				if err != nil || got != want {
					t.Fatalf("Resolve = %+v, %v; want %+v", got, err, want)
				}
			})
		}
	}
}
