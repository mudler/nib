package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ListModels returns the chat models this Copilot account can pick
// (GET {endpoint}/models). Copilot also lists embedding models and internal
// ones the editor's picker hides; only the chat models its picker offers are
// kept, unless the API marks none, in which case every chat model is kept.
func (l *LLM) ListModels(ctx context.Context) ([]string, error) {
	endpoint, err := l.ensureEndpoint(ctx)
	if err != nil {
		return nil, err
	}
	l.metadataMu.Lock()
	defer l.metadataMu.Unlock()
	ids, metadata, err := l.fetchModels(ctx)
	if err == nil {
		l.metadataEndpoint, l.metadataToken = endpoint, l.config.Token
		l.metadata, l.metadataExpires = metadata, time.Now().Add(capabilityTTL)
	}
	return ids, err
}

func (l *LLM) fetchModels(ctx context.Context) ([]string, map[string]modelCapabilities, error) {
	endpoint, err := l.ensureEndpoint(ctx)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/models", nil)
	if err != nil {
		return nil, nil, fmt.Errorf("copilot: create request: %w", err)
	}
	l.setHeaders(req, false)
	req.Header.Del("Content-Type")

	resp, err := l.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("copilot: list models: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxModelsBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("copilot: read models: %w", err)
	}
	if len(body) > maxModelsBytes {
		return nil, nil, fmt.Errorf("copilot: models response exceeds size limit")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, parseAPIError(resp.StatusCode, body)
	}

	var page struct {
		Data []modelCapabilities `json:"data"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, nil, fmt.Errorf("copilot: decode models: %w", err)
	}
	metadata := make(map[string]modelCapabilities)
	var chat, picker []string
	seen := map[string]bool{}
	for _, m := range page.Data {
		if m.ID == "" || seen[m.ID] || (m.Capabilities.Type != "" && m.Capabilities.Type != "chat") {
			continue
		}
		seen[m.ID] = true
		if len(metadata) >= maxCachedModels {
			return nil, nil, fmt.Errorf("copilot: models response exceeds model limit")
		}
		metadata[m.ID] = m
		chat = append(chat, m.ID)
		if m.PickerEnable != nil && *m.PickerEnable {
			picker = append(picker, m.ID)
		}
	}
	if len(picker) > 0 {
		return picker, metadata, nil
	}
	return chat, metadata, nil
}

// Metadata is retained independently of picker visibility. A hidden model may
// still be explicitly selected; discovery is not an entitlement check.
type modelCapabilities struct {
	ID                 string   `json:"id"`
	PickerEnable       *bool    `json:"model_picker_enabled"`
	SupportedEndpoints []string `json:"supported_endpoints"`
	Capabilities       struct {
		Type     string `json:"type"`
		Supports struct {
			Reasoning        *bool `json:"reasoning"`
			ReasoningSummary *bool `json:"reasoning_summary"`
		} `json:"supports"`
		SupportedReasoningEfforts []string `json:"supported_reasoning_efforts"`
	} `json:"capabilities"`
}

const maxModelsBytes = 4 << 20
const maxCachedModels = 512
const capabilityTTL = 5 * time.Minute

// An adapter-local cache avoids sharing credentials or account capabilities
// with picker instances. Identity changes replace, rather than grow, the cache.
// Failures are briefly cached too; cancellation never poisons a later request.
func (l *LLM) modelMetadata(ctx context.Context, endpoint, model string) (modelCapabilities, error) {
	if err := ctx.Err(); err != nil {
		return modelCapabilities{}, err
	}
	l.metadataMu.Lock()
	defer l.metadataMu.Unlock()
	if err := ctx.Err(); err != nil {
		return modelCapabilities{}, err
	}
	if l.metadataEndpoint != endpoint || l.metadataToken != l.config.Token || time.Now().After(l.metadataExpires) {
		discoveryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, metadata, err := l.fetchModels(discoveryCtx)
		cancel()
		if ctx.Err() != nil {
			return modelCapabilities{}, ctx.Err()
		}
		l.metadataEndpoint, l.metadataToken = endpoint, l.config.Token
		l.metadata = metadata
		ttl := capabilityTTL
		if err != nil {
			ttl = 30 * time.Second
		}
		l.metadataExpires = time.Now().Add(ttl)
	}
	return l.metadata[model], nil
}

func (m modelCapabilities) route(model string) (protocolRoute, error) {
	fallback := routeProtocol(model)
	if len(m.SupportedEndpoints) == 0 {
		return fallback, nil
	}
	supported := make(map[protocolRoute]bool)
	for _, endpoint := range m.SupportedEndpoints {
		switch endpoint {
		case "/chat/completions":
			supported[routeChat] = true
		case "/responses":
			supported[routeResponses] = true
		case "/v1/messages":
			supported[routeMessages] = true
		}
	}
	if supported[fallback] {
		return fallback, nil
	}
	for _, route := range []protocolRoute{routeResponses, routeMessages, routeChat} {
		if supported[route] {
			return route, nil
		}
	}
	return "", fmt.Errorf("copilot: model %q has no supported endpoints implemented by this adapter", model)
}
