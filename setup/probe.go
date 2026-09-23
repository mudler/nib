package setup

import (
	"context"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// Probe makes a cheap, token-free connectivity check against the endpoint by
// listing models. It returns nil if the endpoint responds, or a descriptive
// error otherwise. A short timeout keeps the wizard responsive. The model
// argument is accepted for signature stability but not needed by ListModels.
func Probe(ctx context.Context, model, apiKey, baseURL string) error {
	cfg := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	client := openai.NewClientWithConfig(cfg)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := client.ListModels(ctx)
	return err
}
