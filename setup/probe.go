package setup

import (
	"context"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// Probe makes a cheap, token-free connectivity check against the endpoint by
// listing models. It returns nil if the endpoint responds, or a descriptive
// error otherwise. A short timeout keeps the wizard responsive. The model
// argument is accepted for signature stability but not needed by ListModels.
func Probe(ctx context.Context, model, apiKey, baseURL string) error {
	_, err := ProbeModels(ctx, apiKey, baseURL)
	return err
}

// ProbeModels is Probe that also returns the model IDs the endpoint lists.
func ProbeModels(ctx context.Context, apiKey, baseURL string) ([]string, error) {
	cfg := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	client := openai.NewClientWithConfig(cfg)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	list, err := client.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(list.Models))
	for _, m := range list.Models {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// FindClassifierModel returns the first model that looks like a GLiNER
// classifier, which serves the SystemOne API on LocalAI, or "".
func FindClassifierModel(ids []string) string {
	for _, id := range ids {
		if strings.Contains(strings.ToLower(id), "gliner") {
			return id
		}
	}
	return ""
}
