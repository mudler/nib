package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mudler/nib/llmprovider/registry"
	"io"
	"net/http"
	"net/url"
)

// maxModelPages bounds pagination against a server that never stops saying
// has_more; a real model list is one page.
const maxModelPages = 20

// ListModels returns the model IDs the Messages API serves (GET /v1/models),
// following has_more/last_id pagination. Same auth as CreateChatCompletion.
func (l *LLM) ListModels(ctx context.Context) ([]string, error) {
	r := l.DiscoverModels(ctx)
	return r.IDs, r.Err
}

func (l *LLM) DiscoverModels(ctx context.Context) registry.ModelListResult {
	ids, err := l.listModels(ctx)
	return registry.ModelListResult{IDs: ids, Partial: err != nil, Err: err}
}

func (l *LLM) listModels(ctx context.Context) ([]string, error) {
	var ids []string
	seenIDs := map[string]bool{}
	seenCursors := map[string]bool{}
	after := ""
	for range maxModelPages {
		q := url.Values{"limit": {"1000"}}
		if after != "" {
			q.Set("after_id", after)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.config.BaseURL+"/v1/models?"+q.Encode(), nil)
		if err != nil {
			return ids, fmt.Errorf("anthropic: create request: %w", err)
		}
		l.setAuthHeaders(req)

		resp, err := l.client.Do(req)
		if err != nil {
			return ids, fmt.Errorf("anthropic: list models: %w", err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return ids, fmt.Errorf("anthropic: read models: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return ids, parseAPIError(resp.StatusCode, body)
		}

		var page struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return ids, fmt.Errorf("anthropic: decode models: %w", err)
		}
		for _, m := range page.Data {
			if m.ID != "" {
				if !seenIDs[m.ID] {
					seenIDs[m.ID] = true
					ids = append(ids, m.ID)
				}
			}
		}
		if !page.HasMore {
			return ids, nil
		}
		if page.LastID == "" {
			return ids, fmt.Errorf("anthropic: missing model continuation cursor")
		}
		if seenCursors[page.LastID] {
			return ids, fmt.Errorf("anthropic: repeated model cursor")
		}
		seenCursors[page.LastID] = true
		after = page.LastID
	}
	return ids, fmt.Errorf("anthropic: model page limit reached")
}
