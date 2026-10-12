// Package catalog embeds the oh-my-pi model catalog
// as zstd-compressed JSON and provides max-output-token resolution for
// OpenAI-compatible providers.
//
// The catalog is a straight copy of oh-my-pi's models.json — no hand-porting.
// Go's json decoder ignores fields we don't define, so only the fields relevant
// to max_tokens resolution are modelled here. Use gen_catalog.go -refresh to update the catalog and its provenance.
package catalog

// Model is the subset of catalog fields needed for max_tokens resolution.
// All other JSON fields are ignored by the decoder.
type Model struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	API           string `json:"api"`
	Provider      string `json:"provider"`
	BaseURL       string `json:"baseUrl"`
	ContextWindow *int   `json:"contextWindow"`
	MaxTokens     *int   `json:"maxTokens"`
	Compat        Compat `json:"compat"`
}

// Compat holds the compatibility flags that govern how max_tokens is sent
// on the wire. Only the fields relevant to output-token resolution are
// modelled; the full compat object includes additional upstream fields.
type Compat struct {
	// SupportsReasoningSummary is nil when upstream does not specify support.
	SupportsReasoningSummary *bool `json:"supportsReasoningSummary,omitempty"`

	// MaxTokensField selects the wire field name for the output cap:
	// "max_completion_tokens" (most OpenAI-compatible APIs) or "max_tokens"
	// (Mistral, Moonshot, Z.ai, Zhipu, Chutes, Fireworks, DeepSeek).
	MaxTokensField string `json:"maxTokensField"`

	// AlwaysSendMaxTokens forces a cap even when the caller omitted one.
	// For example, Kimi-class models derive TPM limits from it.
	AlwaysSendMaxTokens bool `json:"alwaysSendMaxTokens"`

	// ClampOutputToModelMax uses the model's advertised MaxTokens as the
	// hard ceiling.
	ClampOutputToModelMax bool `json:"clampOutputToModelMax"`

	// OmitMaxOutputTokens suppresses the wire field entirely. Null for most
	// models; true for all ollama-cloud models (proxies whose upstream limit
	// is undiscoverable).
	OmitMaxOutputTokens *bool `json:"omitMaxOutputTokens"`

	// IsOpenRouterHost is true when the provider is OpenRouter or the base
	// URL contains "openrouter.ai". When true and no explicit max_tokens is
	// set, the field is omitted (OpenRouter routing omission).
	IsOpenRouterHost bool `json:"isOpenRouterHost"`

	// SupportedReasoningEfforts is the set of reasoning_effort values the
	// model's backend accepts. When non-empty, a requested effort that the
	// model does not support is clamped down to the nearest supported one.
	SupportedReasoningEfforts []string `json:"supportedReasoningEfforts,omitempty"`
}

// MaxTokensField returns the wire field name for the output cap, defaulting
// to "max_completion_tokens" when unset.
func (c Compat) MaxTokensFieldName() string {
	if c.MaxTokensField != "" {
		return c.MaxTokensField
	}
	return "max_completion_tokens"
}

// ShouldOmitMaxOutputTokens reports whether the wire field should be suppressed
// entirely (omitMaxOutputTokens flag or OpenRouter host without explicit cap).
func (c Compat) ShouldOmitMaxOutputTokens() bool {
	return c.OmitMaxOutputTokens != nil && *c.OmitMaxOutputTokens
}
