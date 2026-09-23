package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// Limits is what the endpoint could tell us about a model: its context window
// and its output cap, each with the source that answered it. A zero value
// means no source knew that limit, and the caller applies its own fallback.
type Limits struct {
	ContextWindow int
	ContextSource string
	OutputCap     int
	OutputSource  string
}

// Source names for Limits, so logs and Resolution.Source say which endpoint a
// limit came from.
const (
	SourceLocalAICapabilities = "localai-capabilities"
	SourceModels              = "models"
	SourceLiteLLMModelInfo    = "litellm-model-info"
)

// limitSource is one endpoint that may know a model's limits. knowsWindow and
// knowsCap say what it can answer at all, so discovery can skip a request that
// could only return a limit it already has.
type limitSource struct {
	name        string
	knowsWindow bool
	knowsCap    bool
	fetch       func(ctx context.Context, baseURL, apiKey, model string) (window, outputCap int, err error)
}

// limitSources is the discovery order. Each provider family puts its limits in
// a different place, and none of them 404s in a way that tells us which family
// we are talking to, so we ask in turn:
//
//   - LocalAI's /models/capabilities reports the context_size the model was
//     actually loaded with, which is the most precise answer anyone gives.
//   - The OpenAI-compatible /models listing carries limits on some providers
//     (context_length, vLLM's max_model_len, the output cap fields).
//   - LiteLLM's /model/info is where a LiteLLM proxy keeps them; its /models
//     listing carries none (regolo is one such proxy).
var limitSources = []limitSource{
	{name: SourceLocalAICapabilities, knowsWindow: true, fetch: fetchLocalAICapabilities},
	{name: SourceModels, knowsWindow: true, knowsCap: true, fetch: fetchModelsEntry},
	{name: SourceLiteLLMModelInfo, knowsWindow: true, knowsCap: true, fetch: fetchLiteLLMModelInfo},
}

// DiscoverLimits asks the endpoint for both of a model's limits, taking each
// from the first source that knows it. It is best-effort: any failure falls
// through to the next source, and the time it may take is bounded by ctx.
func DiscoverLimits(ctx context.Context, baseURL, apiKey, model string) Limits {
	return discoverLimits(ctx, baseURL, apiKey, model, true, true)
}

// DiscoverContextWindow is DiscoverLimits for a caller that only needs the
// window, so it does not pay for requests that could only find an output cap.
func DiscoverContextWindow(ctx context.Context, baseURL, apiKey, model string) (int, string) {
	l := discoverLimits(ctx, baseURL, apiKey, model, true, false)
	return l.ContextWindow, l.ContextSource
}

func discoverLimits(ctx context.Context, baseURL, apiKey, model string, wantWindow, wantCap bool) Limits {
	var l Limits
	if baseURL == "" || model == "" {
		return l
	}
	for _, src := range limitSources {
		needWindow := wantWindow && l.ContextWindow == 0
		needCap := wantCap && l.OutputCap == 0
		if !needWindow && !needCap {
			break
		}
		if (!needWindow || !src.knowsWindow) && (!needCap || !src.knowsCap) {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		window, outputCap, err := src.fetch(ctx, baseURL, apiKey, model)
		if err != nil {
			slog.Debug("model limits discovery failed", "source", src.name, "model", model, "base_url", baseURL, "error", err)
			continue
		}
		if needWindow && window > 0 {
			l.ContextWindow, l.ContextSource = window, src.name
		}
		if needCap && outputCap > 0 {
			l.OutputCap, l.OutputSource = outputCap, src.name
		}
	}
	return l
}

// getJSON fetches {baseURL}{path} and decodes it into out. A non-200 answer is
// an error like any other: to discovery it only means "ask the next source".
func getJSON(ctx context.Context, baseURL, apiKey, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// fetchLocalAICapabilities reads context_size from LocalAI's
// /models/capabilities. It never knows an output cap.
func fetchLocalAICapabilities(ctx context.Context, baseURL, apiKey, model string) (int, int, error) {
	var body struct {
		Data []struct {
			ID          string `json:"id"`
			ContextSize int    `json:"context_size"`
		} `json:"data"`
	}
	if err := getJSON(ctx, baseURL, apiKey, "/models/capabilities", &body); err != nil {
		return 0, 0, err
	}
	for _, m := range body.Data {
		if m.ID == model && m.ContextSize > 0 {
			return m.ContextSize, 0, nil
		}
	}
	return 0, 0, nil
}

func fetchModelsEntry(ctx context.Context, baseURL, apiKey, model string) (int, int, error) {
	info, err := DiscoverModel(ctx, baseURL, apiKey, model)
	if err != nil || info == nil {
		return 0, 0, err
	}
	return info.ContextWindow(), info.OutputCap(), nil
}

// fetchLiteLLMModelInfo reads a LiteLLM proxy's /model/info. The window comes
// from max_input_tokens and the cap from max_output_tokens. LiteLLM's
// max_tokens is ignored on purpose: its model map uses it for the input window
// on some entries and for the output cap on others, and regolo's glm5.2
// reports 200000 there against a 96000 input limit.
//
// max_input_tokens is what the proxy was configured with, not what the backend
// behind it enforces. regolo's vLLM accepts 210000 tokens for glm5.2 while
// LiteLLM reports 96000, so this errs on the small side, which only makes nib
// compact earlier. A window learned from a real overflow error still takes
// precedence over it.
func fetchLiteLLMModelInfo(ctx context.Context, baseURL, apiKey, model string) (int, int, error) {
	var body struct {
		Data []struct {
			ModelName string `json:"model_name"`
			ModelInfo struct {
				// float64 because LiteLLM's model map is Python-sourced and a
				// value written as 96000.0 must not fail the whole decode.
				MaxInputTokens  *float64 `json:"max_input_tokens"`
				MaxOutputTokens *float64 `json:"max_output_tokens"`
			} `json:"model_info"`
		} `json:"data"`
	}
	if err := getJSON(ctx, baseURL, apiKey, "/model/info", &body); err != nil {
		return 0, 0, err
	}
	// A load-balanced model appears once per deployment, and a deployment may
	// leave a field unset, so take each limit from the first entry that has it.
	var window, outputCap int
	for _, e := range body.Data {
		if !strings.EqualFold(e.ModelName, model) {
			continue
		}
		if v := e.ModelInfo.MaxInputTokens; window == 0 && v != nil && *v > 0 {
			window = int(*v)
		}
		if v := e.ModelInfo.MaxOutputTokens; outputCap == 0 && v != nil && *v > 0 {
			outputCap = int(*v)
		}
	}
	return window, outputCap, nil
}
