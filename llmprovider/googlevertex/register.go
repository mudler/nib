package googlevertex

import (
	"fmt"
	"os"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/llmprovider/registry"
	"github.com/mudler/nib/provider"
	"github.com/mudler/nib/types"
)

func init() {
	registry.Register(provider.ProtocolGoogleVertex, factory)
}

func factory(def provider.Definition, config types.ModelProviderConfig, store *auth.Store, _ float32) (cogito.LLM, error) {
	resolved, err := registry.ResolveEndpointCredentials(store, def, config)
	if err != nil {
		return nil, fmt.Errorf("google-vertex: resolve credentials: %w", err)
	}

	apiKey := resolved.APIKey
	isOAuth := resolved.IsOAuth
	token := resolved.APIKey

	// For OAuth auth, require project and location from env vars.
	project := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if project == "" {
		project = os.Getenv("GCP_PROJECT")
	}
	if project == "" {
		project = os.Getenv("GCLOUD_PROJECT")
	}

	location := os.Getenv("GOOGLE_VERTEX_LOCATION")
	if location == "" {
		location = os.Getenv("GOOGLE_CLOUD_LOCATION")
	}
	if location == "" {
		location = os.Getenv("VERTEX_LOCATION")
	}

	if isOAuth && project == "" {
		return nil, fmt.Errorf("google-vertex: OAuth auth requires GOOGLE_CLOUD_PROJECT/GCP_PROJECT/GCLOUD_PROJECT env var")
	}
	if apiKey == "" && !isOAuth {
		return nil, fmt.Errorf("google-vertex: no credentials — set %s or run 'nib login %s'", def.EnvVar, def.ID)
	}

	return New(Config{
		Model:    config.Model,
		BaseURL:  orDefault(config.BaseURL, def.BaseURL),
		APIKey:   apiKey,
		Token:    token,
		IsOAuth:  isOAuth,
		Project:  project,
		Location: location,
	}), nil
}

func orDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
