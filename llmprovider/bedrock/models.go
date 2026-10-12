package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mudler/nib/llmprovider/registry"
	"io"
	"net/http"
	"net/url"
	"slices"
)

// maxModelPages bounds nextToken pagination.
const maxModelPages = 20

// controlPlaneURL returns the Bedrock control-plane endpoint for region. Model
// listing lives there (service "bedrock"), not on bedrock-runtime where chat
// requests go. A variable so tests can point it at a fake.
var controlPlaneURL = func(region string) string {
	return "https://bedrock." + region + ".amazonaws.com"
}

// ListModels returns the model IDs Converse-stream can call in this region:
// system inference profiles first (newer models, Claude 3.7+ among them, are
// only reachable through one, e.g. "us.anthropic.claude-…"), then active
// foundation models that stream text on demand.
func (l *LLM) ListModels(ctx context.Context) ([]string, error) {
	r := l.DiscoverModels(ctx)
	return r.IDs, r.Err
}

func (l *LLM) DiscoverModels(ctx context.Context) registry.ModelListResult {
	ids, err := l.listModels(ctx)
	return registry.ModelListResult{IDs: ids, Partial: err != nil, Err: err}
}

func (l *LLM) listModels(ctx context.Context) ([]string, error) {
	base := controlPlaneURL(l.config.Region)
	var diagnostic error
	var ids []string
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}

	// Profiles are best-effort: an identity may be allowed to list models but
	// not profiles, and the foundation models are still worth offering.
	next := ""
	seenCursors := map[string]bool{}
	profilesComplete := false
	for range maxModelPages {
		q := url.Values{"typeEquals": {"SYSTEM_DEFINED"}, "maxResults": {"1000"}}
		if next != "" {
			q.Set("nextToken", next)
		}
		var page struct {
			Profiles []struct {
				ID     string `json:"inferenceProfileId"`
				Status string `json:"status"`
			} `json:"inferenceProfileSummaries"`
			NextToken string `json:"nextToken"`
		}
		if err := l.controlPlaneGet(ctx, base, "/inference-profiles", q, &page); err != nil {
			diagnostic = fmt.Errorf("bedrock: inference profiles: %w", err)
			break
		}
		for _, p := range page.Profiles {
			if p.Status == "" || p.Status == "ACTIVE" {
				add(p.ID)
			}
		}
		if page.NextToken == "" {
			profilesComplete = true
			break
		}
		if seenCursors[page.NextToken] {
			diagnostic = fmt.Errorf("bedrock: repeated profile cursor")
			break
		}
		seenCursors[page.NextToken] = true
		next = page.NextToken
	}

	if !profilesComplete && diagnostic == nil {
		diagnostic = fmt.Errorf("bedrock: profile page limit reached")
	}
	q := url.Values{"byOutputModality": {"TEXT"}, "byInferenceType": {"ON_DEMAND"}}
	var fm struct {
		Models []struct {
			ID        string   `json:"modelId"`
			Streaming *bool    `json:"responseStreamingSupported"`
			Inference []string `json:"inferenceTypesSupported"`
			Lifecycle struct {
				Status string `json:"status"`
			} `json:"modelLifecycle"`
		} `json:"modelSummaries"`
	}
	if err := l.controlPlaneGet(ctx, base, "/foundation-models", q, &fm); err != nil {
		return ids, errors.Join(diagnostic, fmt.Errorf("bedrock: foundation models: %w", err))
	}
	for _, m := range fm.Models {
		if m.Streaming != nil && !*m.Streaming {
			continue // converse-stream needs streaming
		}
		if m.Lifecycle.Status != "" && m.Lifecycle.Status != "ACTIVE" {
			continue
		}
		if len(m.Inference) > 0 && !slices.Contains(m.Inference, "ON_DEMAND") {
			continue
		}
		add(m.ID)
	}
	return ids, diagnostic
}

// controlPlaneGet performs a GET on the Bedrock control plane with the same
// auth as chat (Bedrock API key as Bearer, else SigV4 for service "bedrock")
// and decodes the JSON response into out.
func (l *LLM) controlPlaneGet(ctx context.Context, base, path string, q url.Values, out any) error {
	u, err := url.Parse(base + path + "?" + q.Encode())
	if err != nil {
		return fmt.Errorf("bedrock: models url: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("bedrock: create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if l.config.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+l.config.BearerToken)
	} else {
		creds, err := resolveCredentials(l.config.Profile)
		if err != nil {
			return err
		}
		signed := signRequest(signParams{
			Method:  http.MethodGet,
			Host:    u.Host,
			Path:    u.EscapedPath(),
			Query:   u.RawQuery,
			Headers: map[string]string{"accept": "application/json"},
			Region:  l.config.Region,
			Service: "bedrock",
			Creds:   creds,
		})
		req.Header.Set("Host", signed.Host)
		req.Header.Set("X-Amz-Date", signed.AmzDate)
		req.Header.Set("X-Amz-Content-Sha256", signed.AmzContentSHA256)
		req.Header.Set("Authorization", signed.Authorization)
		if signed.AmzSecurityToken != "" {
			req.Header.Set("X-Amz-Security-Token", signed.AmzSecurityToken)
		}
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return fmt.Errorf("bedrock: list models: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("bedrock: read models: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return parseAPIError(resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("bedrock: decode models: %w", err)
	}
	return nil
}
