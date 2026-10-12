package googlevertex

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mudler/nib/llmprovider/registry"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/mudler/nib/llmprovider/google"
)

// maxModelPages bounds pagination against a server that keeps returning a
// nextPageToken.
const maxModelPages = 20

// ListModels returns the Gemini models Google publishes on Vertex AI
// (GET {base}/v1beta1/publishers/google/models; the list call only exists in
// v1beta1). The catalogue also carries Imagen, Veo, embedding and other
// non-chat models this adapter cannot drive, so only Gemini chat models are
// kept. OAuth callers bill the listing to their project via
// x-goog-user-project, as the chat call does through its URL.
//
// The listing refuses API keys ("API keys are not supported by this API"), so
// an API-key (express mode) session is offered google.KnownChatModels instead.
func (l *LLM) ListModels(ctx context.Context) ([]string, error) {
	r := l.DiscoverModels(ctx)
	return r.IDs, r.Err
}

func (l *LLM) DiscoverModels(ctx context.Context) registry.ModelListResult {
	ids, err := l.listModels(ctx)
	return registry.ModelListResult{IDs: ids, Partial: err != nil || !l.config.IsOAuth, Err: err}
}

func (l *LLM) listModels(ctx context.Context) ([]string, error) {
	if !l.config.IsOAuth {
		// Only the official express-mode endpoint gets vendor suggestions.
		// A custom endpoint has no known API-key listing contract.
		if strings.TrimRight(l.config.BaseURL, "/") != "https://aiplatform.googleapis.com" {
			return nil, registry.ErrNoModelList
		}
		return google.KnownChatModels(), nil
	}
	base := strings.TrimRight(l.config.BaseURL, "/")
	var ids []string
	seenIDs := map[string]bool{}
	seenCursors := map[string]bool{}
	token := ""
	for range maxModelPages {
		q := url.Values{"pageSize": {"1000"}}
		if token != "" {
			q.Set("pageToken", token)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1beta1/publishers/google/models?"+q.Encode(), nil)
		if err != nil {
			return ids, fmt.Errorf("google-vertex: create request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		if l.config.IsOAuth {
			req.Header.Set("Authorization", "Bearer "+l.config.Token)
			if l.config.Project != "" {
				req.Header.Set("x-goog-user-project", l.config.Project)
			}
		} else {
			req.Header.Set("x-goog-api-key", l.config.APIKey)
		}

		resp, err := l.client.Do(req)
		if err != nil {
			return ids, fmt.Errorf("google-vertex: list models: %w", err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return ids, fmt.Errorf("google-vertex: read models: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return ids, parseAPIError(resp.StatusCode, body)
		}

		var page struct {
			PublisherModels []struct {
				Name string `json:"name"`
			} `json:"publisherModels"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return ids, fmt.Errorf("google-vertex: decode models: %w", err)
		}
		for _, m := range page.PublisherModels {
			id := m.Name[strings.LastIndex(m.Name, "/")+1:]
			if strings.HasPrefix(id, "gemini") && !strings.Contains(id, "embedding") {
				if !seenIDs[id] {
					seenIDs[id] = true
					ids = append(ids, id)
				}
			}
		}
		if page.NextPageToken == "" {
			return ids, nil
		}
		if seenCursors[page.NextPageToken] {
			return ids, fmt.Errorf("googlevertex: repeated model cursor")
		}
		seenCursors[page.NextPageToken] = true
		token = page.NextPageToken
	}
	return ids, fmt.Errorf("googlevertex: model page limit reached")
}

// ModelListIsPartial: in API-key mode the list is google.KnownChatModels, a
// suggestion; with OAuth it is Vertex's own catalogue.
func (l *LLM) ModelListIsPartial() bool { return !l.config.IsOAuth }
