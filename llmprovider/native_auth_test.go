package llmprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/llmprovider/registry"
	"github.com/mudler/nib/provider"
	"github.com/mudler/nib/types"
)

// Exercise registered factories, not constructors that bypass credential resolution.
func TestNativeCustomEndpointCredentials(t *testing.T) {
	for _, id := range []string{"anthropic-api-key", "anthropic", "google", "google-vertex", "zai"} {
		t.Run(id, func(t *testing.T) {
			def, _ := provider.Get(id)
			factory, _ := registry.Get(def.Protocol)
			t.Setenv(def.EnvVar, "sentinel-env")
			for _, stored := range []auth.CredentialKind{"", auth.CredentialAPIKey, auth.CredentialOAuth} {
				t.Run(string(stored), func(t *testing.T) {
					store := auth.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
					if stored != "" {
						if err := store.Save(auth.Credential{ProviderID: id, Kind: stored, APIKey: "sentinel-store", AccessToken: "sentinel-oauth"}); err != nil {
							t.Fatal(err)
						}
					}
					for _, key := range []string{"", "endpoint-key"} {
						t.Run("key="+key, func(t *testing.T) {
							srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								if r.Header.Get("Authorization") != "" {
									t.Errorf("unexpected bearer auth: %v", r.Header)
								}
								header := "x-goog-api-key"
								if def.Protocol == provider.ProtocolAnthropicMessages {
									header = "x-api-key"
								}
								if got := r.Header.Get(header); got != key {
									t.Errorf("key=%q want %q", got, key)
								}
								w.Write([]byte(`{}`))
							}))
							defer srv.Close()
							llm, err := factory(def, types.ModelProviderConfig{BaseURL: srv.URL, APIKey: key}, store, 0)
							if key == "" {
								if err == nil {
									t.Fatal("custom endpoint without explicit key accepted ambient credentials")
								}
								return
							}
							if err != nil {
								t.Fatal(err)
							}
							// Vertex API-key discovery deliberately does not send HTTP requests;
							// inspect resolved config too, covering the credentials used by inference.
							cfg := reflect.ValueOf(llm).Elem().FieldByName("config")
							if cfg.FieldByName("APIKey").String() != key || cfg.FieldByName("Token").String() != key || cfg.FieldByName("IsOAuth").Bool() {
								t.Fatal("custom config inherited vendor credentials")
							}
							llm.(registry.ModelDiscoverer).DiscoverModels(context.Background())
						})
					}
				})
			}
			// A registered compatible provider's own URL is not a custom override.
			for _, base := range []string{"", def.BaseURL, def.BaseURL + "/"} {
				llm, err := factory(def, types.ModelProviderConfig{BaseURL: base}, nil, 0)
				if err != nil {
					t.Fatal(err)
				}
				if got := reflect.ValueOf(llm).Elem().FieldByName("config").FieldByName("APIKey").String(); got != "sentinel-env" {
					t.Fatalf("official key=%q", got)
				}
			}
		})
	}
}
