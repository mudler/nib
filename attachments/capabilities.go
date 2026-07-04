package attachments

import (
	"context"
	"encoding/json"
	"net/http"
)

func textOnly() ModelCapabilities { return ModelCapabilities{InputModalities: []string{"text"}} }

// FetchCapabilities asks LocalAI's /models/capabilities endpoint for the given
// model's input modalities. Any failure (network, decode, unknown model) is
// treated as text-only so we never hand media to a model that can't take it.
func FetchCapabilities(ctx context.Context, baseURL, apiKey, model string) ModelCapabilities {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models/capabilities", nil)
	if err != nil {
		return textOnly()
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return textOnly()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return textOnly()
	}
	var body struct {
		Data []struct {
			ID              string   `json:"id"`
			InputModalities []string `json:"input_modalities"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return textOnly()
	}
	for _, m := range body.Data {
		if m.ID == model {
			if len(m.InputModalities) == 0 {
				return textOnly()
			}
			return ModelCapabilities{InputModalities: m.InputModalities}
		}
	}
	return textOnly()
}
