package catalog

import (
	"context"
	"net/url"
	"strings"

	"github.com/mudler/nib/types"
)

// FallbackMaxTokens is the final fallback when discovery and catalog both
// come up empty. This matches cogito's defaultMaxTokens const and nib's
// native adapter defaults.
const FallbackMaxTokens = 16384

// Resolution is the result of resolving max output tokens for a model.
type Resolution struct {
	// MaxTokens is the resolved output token cap to send on the wire.
	// 0 means "do not send the field" (omitMaxOutputTokens case).
	MaxTokens int

	// Source describes where the value came from, for debugging.
	Source string

	// WireField is the JSON field name to use: "max_tokens" or
	// "max_completion_tokens".
	WireField string

	// Omit reports whether the wire field should be suppressed entirely.
	Omit bool
}

// ResolveMaxTokens runs the full resolution chain:
//  1. User-explicit config.MaxTokens (always wins if >0)
//  2. API discovery (DiscoverLimits: the provider's /models, then LiteLLM's /model/info)
//  3. Catalog lookup (oh-my-pi model entry)
//  4. FallbackMaxTokens (16384)
//
// The catalog's compat flags modify how the value is applied:
//   - omitMaxOutputTokens: suppress the field entirely (Omit=true)
//   - isOpenRouterHost: omit when no explicit user value
//   - clampOutputToModelMax: clamp to the catalog's maxTokens if smaller
//   - maxTokensField: selects "max_tokens" vs "max_completion_tokens"
func ResolveMaxTokens(ctx context.Context, config types.ModelProviderConfig, baseURL, apiKey string) Resolution {
	// 1. User-explicit setting always wins.
	if config.MaxTokens > 0 {
		res := Resolution{
			MaxTokens: config.MaxTokens,
			Source:    "user-config",
			WireField: "max_completion_tokens",
		}
		applyCatalogCompat(&res, config, baseURL)
		return res
	}

	// 2. API discovery. /models has always answered here under the plain
	// "api-discovery" source; another endpoint names itself in the suffix.
	if ctx != nil && baseURL != "" && config.Model != "" {
		if l := discoverLimits(ctx, baseURL, apiKey, config.Model, false, true); l.OutputCap > 0 {
			source := "api-discovery"
			if l.OutputSource != SourceModels {
				source += ":" + l.OutputSource
			}
			res := Resolution{
				MaxTokens: l.OutputCap,
				Source:    source,
				WireField: "max_completion_tokens",
			}
			applyCatalogCompat(&res, config, baseURL)
			return res
		}
	}

	// 3. Catalog lookup.
	if m, ok := Lookup(config.Provider, config.Model, baseURL); ok {
		res := Resolution{
			MaxTokens: FallbackMaxTokens,
			Source:    "catalog-fallback",
			WireField: m.Compat.MaxTokensFieldName(),
		}

		// If the catalog has an explicit maxTokens, use it.
		if m.MaxTokens != nil && *m.MaxTokens > 0 {
			res.MaxTokens = *m.MaxTokens
			res.Source = "catalog"
		}

		// Clamp to model max if the flag is set and our value exceeds it.
		if m.Compat.ClampOutputToModelMax && m.MaxTokens != nil && *m.MaxTokens > 0 {
			if res.MaxTokens > *m.MaxTokens {
				res.MaxTokens = *m.MaxTokens
			}
		}

		applyCompatFlags(&res, m.Compat, baseURL)
		return res
	}

	// 4. Final fallback.
	res := Resolution{
		MaxTokens: FallbackMaxTokens,
		Source:    "default",
		WireField: "max_completion_tokens",
	}
	// Check if this is an OpenRouter host even without a catalog entry.
	if isOpenRouterHost(baseURL) {
		res.Omit = true
		res.Source = "default-omit-openrouter"
	}
	return res
}

// applyCatalogCompat looks up the model in the catalog to apply compat flags
// (wire field name, omit, clamp) to an already-resolved value.
func applyCatalogCompat(res *Resolution, config types.ModelProviderConfig, baseURL string) {
	if m, ok := Lookup(config.Provider, config.Model, baseURL); ok {
		res.WireField = m.Compat.MaxTokensFieldName()

		// Clamp to model max if the flag is set.
		if m.Compat.ClampOutputToModelMax && m.MaxTokens != nil && *m.MaxTokens > 0 {
			if res.MaxTokens > *m.MaxTokens {
				res.MaxTokens = *m.MaxTokens
			}
		}

		applyCompatFlags(res, m.Compat, baseURL)
	}
}

// applyCompatFlags applies the omit and openrouter-host flags.
func applyCompatFlags(res *Resolution, compat Compat, baseURL string) {
	if compat.ShouldOmitMaxOutputTokens() {
		res.Omit = true
		return
	}
	// OpenRouter hosts: omit the field unless the user explicitly set one.
	// (User-explicit values are handled by the caller — when we get here from
	// discovery or catalog, there's no explicit user value, so we omit.)
	if compat.IsOpenRouterHost || isOpenRouterHost(baseURL) {
		res.Omit = true
	}
}

func isOpenRouterHost(baseURL string) bool {
	if baseURL == "" {
		return false
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(u.Host), "openrouter.ai")
}
