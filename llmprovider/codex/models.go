package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/mudler/nib/llmprovider/catalog"
	"github.com/mudler/nib/provider"
)

// codexModelsEndpoint lists the models the ChatGPT subscription can use with
// Codex, the same list the Codex CLI's /model picker shows.
const codexModelsEndpoint = "/codex/models"

const sourceCodexModels = "codex-models"

// modelsBaseURL is codexBaseURL, a variable so tests can point it at a fake.
var modelsBaseURL = codexBaseURL

type codexModel struct {
	Slug             string `json:"slug"`
	ID               string `json:"id"`
	Visibility       string `json:"visibility"`
	ContextWindow    int    `json:"context_window"`
	MaxContextWindow int    `json:"max_context_window"`
}

type codexModelsPage struct {
	Models []codexModel `json:"models"`
}

// ListModels preserves the account's advertised models and appends conservative
// catalog suggestions. Suggestions do not establish account access. Entries
// the backend marks hidden are never added back from the catalog.
func (l *LLM) ListModels(ctx context.Context) ([]string, error) {
	page, err := l.fetchModels(ctx)
	if err != nil {
		return nil, err
	}
	var ids []string
	seen, hidden := map[string]bool{}, map[string]bool{}
	for _, m := range page.Models {
		if modelHidden(m.Visibility) {
			hidden[strings.ToLower(firstNonEmpty(m.Slug, m.ID))] = true
		}
	}
	for _, m := range page.Models {
		id := firstNonEmpty(m.Slug, m.ID)
		if id == "" || seen[id] || hidden[strings.ToLower(id)] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if !l.catalogSuggestions {
		return ids, nil
	}
	// Official Codex model guidance: https://learn.chatgpt.com/docs/models .
	// Keep this allowlist narrow: the provider catalog also has image-only and
	// restricted models. Resolve metadata only on the exact ChatGPT route.
	for _, id := range []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6-luna"} {
		if seen[id] || hidden[strings.ToLower(id)] {
			continue
		}
		m, ok := catalog.Lookup("openai-codex", id, "")
		if !ok || m.Provider != "openai-codex" || m.API != string(provider.ProtocolCodexResponses) || strings.TrimRight(m.BaseURL, "/") != codexBaseURL {
			continue
		}
		ids = append(ids, m.ID)
	}

	return ids, nil
}

// ModelListIsPartial allows exact typed IDs outside the suggestions. A model
// list is not an account entitlement check; the provider decides on inference.
func (l *LLM) ModelListIsPartial() bool { return l.catalogSuggestions }

// ModelLimits returns model metadata from ChatGPT's native Codex model catalog.
func (l *LLM) ModelLimits(ctx context.Context, model string) (catalog.Limits, error) {
	page, err := l.fetchModels(ctx)
	if err != nil {
		return catalog.Limits{}, err
	}
	for _, m := range page.Models {
		if modelHidden(m.Visibility) || !strings.EqualFold(firstNonEmpty(m.Slug, m.ID), model) {
			continue
		}
		window := m.ContextWindow
		if window <= 0 {
			window = m.MaxContextWindow
		}
		if window > 0 {
			return catalog.Limits{ContextWindow: window, ContextSource: sourceCodexModels}, nil
		}
		return catalog.Limits{}, nil
	}
	return catalog.Limits{}, nil
}

func (l *LLM) fetchModels(ctx context.Context) (codexModelsPage, error) {
	u := modelsBaseURL + codexModelsEndpoint + "?" + url.Values{"client_version": {codexClientVer}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return codexModelsPage{}, fmt.Errorf("codex: create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+l.config.Token)
	req.Header.Set("originator", "nib")
	req.Header.Set("version", codexClientVer)
	if accountID := extractAccountID(l.config.Token); accountID != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return codexModelsPage{}, fmt.Errorf("codex: list models: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return codexModelsPage{}, fmt.Errorf("codex: read models: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return codexModelsPage{}, parseAPIError(resp.StatusCode, body)
	}

	var page codexModelsPage
	if err := json.Unmarshal(body, &page); err != nil {
		return codexModelsPage{}, fmt.Errorf("codex: decode models: %w", err)
	}
	return page, nil
}

func modelHidden(visibility string) bool {
	return strings.EqualFold(visibility, "hide") || strings.EqualFold(visibility, "none")
}
