// Package catalog queries OpenAI-compatible API endpoints to discover
// per-model output token limits at runtime.
//
// This mirrors maki's approach: GET {baseURL}/models, then read
// max_completion_tokens (or max_tokens) and context_length from the model
// entry. Many vLLM-based providers (e.g. regolo) expose accurate limits here
// that no static catalog carries.
package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ModelInfo is the subset of a /models entry we care about.
type ModelInfo struct {
	ID                  string `json:"id"`
	MaxCompletionTokens *int   `json:"max_completion_tokens"`
	MaxTokens           *int   `json:"max_tokens"`
	ContextLength       *int   `json:"context_length"`
	MaxOutputTokens     *int   `json:"max_output_tokens"`
	// MaxModelLen is vLLM's name for the context window in its /models
	// listing.
	MaxModelLen *int `json:"max_model_len"`
}

// OutputCap returns the discovered output token cap, or 0 if unknown.
// It checks fields in priority order: max_output_tokens (regolo/GLM),
// max_completion_tokens (vLLM standard), max_tokens (OpenAI legacy).
func (m ModelInfo) OutputCap() int {
	if m.MaxOutputTokens != nil && *m.MaxOutputTokens > 0 {
		return *m.MaxOutputTokens
	}
	if m.MaxCompletionTokens != nil && *m.MaxCompletionTokens > 0 {
		return *m.MaxCompletionTokens
	}
	if m.MaxTokens != nil && *m.MaxTokens > 0 {
		return *m.MaxTokens
	}
	return 0
}

// ContextWindow returns the discovered context window, or 0 if unknown.
func (m ModelInfo) ContextWindow() int {
	if m.ContextLength != nil && *m.ContextLength > 0 {
		return *m.ContextLength
	}
	if m.MaxModelLen != nil && *m.MaxModelLen > 0 {
		return *m.MaxModelLen
	}
	return 0
}

type modelsResponse struct {
	Data []ModelInfo `json:"data"`
	// Some providers return a bare array instead of {data: [...]}.
	// We handle that in DiscoverModel.
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

// DiscoverModel queries {baseURL}/models and returns the entry matching modelID.
// Returns nil, nil when the model is not found in the listing (no error).
func DiscoverModel(ctx context.Context, baseURL, apiKey, modelID string) (*ModelInfo, error) {
	url := strings.TrimRight(baseURL, "/") + "/models"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("discovery: new request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discovery: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	var wrapper modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return nil, nil
	}

	for i := range wrapper.Data {
		if strings.EqualFold(wrapper.Data[i].ID, modelID) {
			return &wrapper.Data[i], nil
		}
	}
	return nil, nil
}
