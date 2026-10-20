package setup

import (
	"context"
	"time"

	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/llmprovider"
	"github.com/mudler/nib/types"
)

// Probe preserves the legacy OpenAI-compatible connectivity check.
func Probe(ctx context.Context, model, apiKey, baseURL string) error {
	return ProbeConfig(ctx, types.ModelProviderConfig{Model: model, APIKey: apiKey, BaseURL: baseURL}, nil)
}

// ProbeConfig checks connectivity with token-free model discovery, using the
// selected provider's native protocol and scoped credentials, never inference.
func ProbeConfig(ctx context.Context, cfg types.ModelProviderConfig, store *auth.Store) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := llmprovider.ListModels(ctx, cfg, store)
	return err
}
