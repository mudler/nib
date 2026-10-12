package google

import (
	"fmt"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/llmprovider/registry"
	"github.com/mudler/nib/provider"
	"github.com/mudler/nib/types"
)

func init() {
	registry.Register(provider.ProtocolGoogleGemini, factory)
}

func factory(def provider.Definition, config types.ModelProviderConfig, store *auth.Store, _ float32) (cogito.LLM, error) {
	resolved, err := registry.ResolveEndpointCredentials(store, def, config)
	if err != nil {
		return nil, fmt.Errorf("google: resolve credentials: %w", err)
	}
	if resolved.APIKey == "" {
		return nil, fmt.Errorf("google: no credentials — run 'nib login %s' or set %s", def.ID, def.EnvVar)
	}
	return New(Config{
		Model:   config.Model,
		BaseURL: orDefault(config.BaseURL, def.BaseURL),
		APIKey:  resolved.APIKey,
		Token:   resolved.APIKey,
		IsOAuth: resolved.IsOAuth,
	}), nil
}

func orDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
