package chat

import (
	"context"
	"strings"
	"time"

	"github.com/mudler/nib/llmprovider/catalog"
	"github.com/mudler/xlog"
)

// defaultContextTokens is the last-resort fallback when neither the endpoint
// probe nor the static table can identify the model's context window.
const defaultContextTokens = 128000

// staticContextSize returns a known context window for commercial models that
// do not expose /models/capabilities. Matching is case-insensitive on the
// model name prefix. Returns 0 when the model is not recognised.
func staticContextSize(model string) int {
	m := strings.ToLower(strings.TrimSpace(model))
	for _, e := range staticContextTable {
		if strings.HasPrefix(m, e.prefix) {
			return e.tokens
		}
	}
	return 0
}

type contextEntry struct {
	prefix string
	tokens int
}

// staticContextTable is ordered longest-prefix-first so that e.g. "gpt-4o"
// matches before "gpt-4".
var staticContextTable = []contextEntry{
	// OpenAI
	{"gpt-4o", 128000},
	{"gpt-4-turbo", 128000},
	{"gpt-4.1", 1047576},
	{"gpt-4", 8192},
	{"gpt-3.5", 16385},
	{"o1", 200000},
	{"o3", 200000},
	{"o4", 200000},

	// Anthropic
	{"claude-3", 200000},
	{"claude-2", 100000},

	// Google
	{"gemini-2", 1048576},
	{"gemini-1.5", 1048576},
}

// detectContextSize resolves the model's context window by asking the
// endpoint first (catalog.DiscoverContextWindow: LocalAI, then /models, then
// LiteLLM), then the static table. Returns 0 when neither source identifies
// the model, leaving the caller to apply its own default.
func detectContextSize(ctx context.Context, baseURL, apiKey, model string) int {
	if v, source := catalog.DiscoverContextWindow(ctx, baseURL, apiKey, model); v > 0 {
		xlog.Debug("context window discovered", "model", model, "tokens", v, "source", source)
		return v
	}
	return staticContextSize(model)
}

// probeTimeout bounds the context-size probe. Like ModelListTimeout this runs
// on a path where blocking degrades the UX; a failed probe is silent (the
// existing value is kept), so a short budget is strictly better than waiting.
const probeTimeout = 5 * time.Second
