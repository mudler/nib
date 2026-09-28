// Package llmprovider constructs cogito LLM transports from nib configuration.
//
// Native adapters (anthropic, google) self-register via llmprovider/registry
// in their init(). This file registers the OpenAI-compatible factory and
// provides the top-level New / NewWithStore constructors.
//
// Adding a new native protocol: write the adapter package, call
// registry.Register in init(), and blank-import it below. No changes to
// NewWithTemperatureAndStore are needed.
package llmprovider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mudler/cogito"
	"github.com/mudler/cogito/clients"
	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/codexapp"
	"github.com/mudler/nib/llmprovider/catalog"
	"github.com/mudler/nib/llmprovider/registry"
	"github.com/mudler/nib/provider"
	"github.com/mudler/nib/types"
	openai "github.com/sashabaranov/go-openai"

	// Blank imports trigger init() self-registration.
	_ "github.com/mudler/nib/llmprovider/anthropic"
	_ "github.com/mudler/nib/llmprovider/azureresponses"
	_ "github.com/mudler/nib/llmprovider/bedrock"
	_ "github.com/mudler/nib/llmprovider/codex"
	_ "github.com/mudler/nib/llmprovider/copilot"
	_ "github.com/mudler/nib/llmprovider/geminicli"
	_ "github.com/mudler/nib/llmprovider/google"
	_ "github.com/mudler/nib/llmprovider/googlevertex"
	_ "github.com/mudler/nib/llmprovider/ollama"
	_ "github.com/mudler/nib/llmprovider/openairesponses"
)

const (
	ProviderOpenAI = "openai"
	ProviderCodex  = "codex"
)

func init() {
	registry.Register(provider.ProtocolOpenAICompletions, openAIFactory)
}

// openAIFactory builds a LocalAI (OpenAI-compatible) LLM for any provider
// whose protocol is ProtocolOpenAICompletions. Credentials are resolved
// from the store / config / env ladder.
func openAIFactory(def provider.Definition, config types.ModelProviderConfig, store *auth.Store, temperature float32) (cogito.LLM, error) {
	baseURL, apiKey := openAIEndpoint(def, config, store)
	llm := clients.NewLocalAILLM(config.Model, apiKey, baseURL)
	llm.SetTemperature(temperature)
	llm.SetMetadata(config.Metadata)
	llm.SetReasoningEffort(config.ReasoningEffort)

	// Resolve max output tokens: user config → catalog → default. The nil
	// context skips the discovery request on purpose: building a client must
	// not talk to the network, or starting a session and switching a model
	// both block on a round-trip and fail where there is none. The session
	// runs discovery on the first turn instead and applies what it finds
	// through SetMaxTokens (see chat/modellimits.go).
	if baseURL == "" {
		baseURL = openAIDefaultBaseURL
	}
	res := catalog.ResolveMaxTokens(nil, config, baseURL, apiKey)
	if !res.Omit && res.MaxTokens > 0 {
		llm.SetMaxTokens(res.MaxTokens)
	}

	return llm, nil
}

// openAIEndpoint returns the base URL and key an OpenAI-compatible request for
// config should use.
//
// A config base URL that points somewhere other than the provider's own
// endpoint is a custom server (LocalAI, vLLM, a proxy): it gets only the key
// configured alongside it. Otherwise a key stored by /login for, say, OpenAI —
// or a stray OPENAI_API_KEY — would be sent to whatever server base_url names,
// since an unset provider resolves to the "openai" definition.
func openAIEndpoint(def provider.Definition, config types.ModelProviderConfig, store *auth.Store) (baseURL, apiKey string) {
	baseURL = orDefault(config.BaseURL, def.BaseURL)
	if isCustomEndpoint(def, config.BaseURL) {
		return baseURL, config.APIKey
	}
	resolved, _ := auth.Resolve(store, def, config.APIKey)
	return baseURL, resolved.APIKey
}

func isCustomEndpoint(def provider.Definition, configured string) bool {
	configured = strings.TrimRight(strings.TrimSpace(configured), "/")
	return configured != "" && configured != strings.TrimRight(def.BaseURL, "/")
}

// ErrNoModelList reports a provider whose protocol has no model listing nib
// can query. A UI should let the user type a model name instead.
var ErrNoModelList = registry.ErrNoModelList

// ModelsEndpoint returns the OpenAI-compatible base URL (the one /models hangs
// off) and key for config, resolving credentials the same way the LLM client
// does. Protocols without such an endpoint return ErrNoModelList.
func ModelsEndpoint(config types.ModelProviderConfig, store *auth.Store) (baseURL, apiKey string, err error) {
	def, ok := provider.Get(normalize(config.Provider))
	if !ok {
		return "", "", fmt.Errorf("unknown LLM provider %q", config.Provider)
	}
	switch def.Protocol {
	case provider.ProtocolOpenAICompletions:
		baseURL, apiKey = openAIEndpoint(def, config, store)
		if baseURL == "" {
			baseURL = openAIDefaultBaseURL
		}
		return baseURL, apiKey, nil
	case provider.ProtocolOllamaChat:
		// Ollama serves an OpenAI-compatible /v1 next to its native API.
		resolved, _ := auth.Resolve(store, def, config.APIKey)
		return strings.TrimRight(orDefault(config.BaseURL, def.BaseURL), "/") + "/v1", resolved.APIKey, nil
	}
	return "", "", ErrNoModelList
}

const openAIDefaultBaseURL = "https://api.openai.com/v1"

// ModelLister is implemented by native adapters that can say which models
// they serve, by asking their API or from a built-in list (see
// registry.PartialModelList).
type ModelLister interface {
	ListModels(ctx context.Context) ([]string, error)
}

type ModelLimitsProvider interface {
	ModelLimits(ctx context.Context, model string) (catalog.Limits, error)
}

// ListModels returns the model IDs config's provider serves. OpenAI-compatible
// endpoints (and Ollama's /v1) are queried at /models; native protocols are
// asked through their adapter, with its own auth. Anything else returns
// ErrNoModelList.
func ListModels(ctx context.Context, config types.ModelProviderConfig, store *auth.Store) ([]string, error) {
	ids, _, err := ListModelChoices(ctx, config, store)
	return ids, err
}

// ListModelChoices is ListModels plus whether the list is partial: a
// suggestion (see registry.PartialModelList) that a name outside it may still
// be valid for, so a UI should accept typed names instead of refusing them.
func ListModelChoices(ctx context.Context, config types.ModelProviderConfig, store *auth.Store) (ids []string, partial bool, err error) {
	baseURL, apiKey, err := ModelsEndpoint(config, store)
	if err == nil {
		cfg := openai.DefaultConfig(apiKey)
		cfg.BaseURL = baseURL
		resp, err := openai.NewClientWithConfig(cfg).ListModels(ctx)
		if err != nil {
			return nil, false, err
		}
		models := make([]string, 0, len(resp.Models))
		for _, m := range resp.Models {
			if m.ID != "" {
				models = append(models, m.ID)
			}
		}
		return models, false, nil
	}
	if !errors.Is(err, ErrNoModelList) {
		return nil, false, err
	}
	// Building the adapter makes no request; it resolves credentials, so a
	// missing key surfaces here as the adapter's own "no credentials" error.
	llm, err := NewWithStore(config, store)
	if err != nil {
		return nil, false, err
	}
	lister, ok := llm.(ModelLister)
	if !ok {
		return nil, false, ErrNoModelList
	}
	ids, err = lister.ListModels(ctx)
	if p, ok := llm.(registry.PartialModelList); ok {
		partial = p.ModelListIsPartial()
	}
	return ids, partial, err
}

// New returns an independent LLM transport. OpenAI means any
// OpenAI-compatible HTTP endpoint, including a local LocalAI server.
func New(config types.ModelProviderConfig) (cogito.LLM, error) {
	return NewWithTemperature(config, 0)
}

// NewWithTemperature applies agent-specific sampling to OpenAI-compatible
// providers. Codex app-server owns its reasoning configuration and ignores the
// compatibility hint.
func NewWithTemperature(config types.ModelProviderConfig, temperature float32) (cogito.LLM, error) {
	return NewWithTemperatureAndStore(config, temperature, nil)
}

// NewWithStore is like New but resolves credentials from store for providers
// that support /login (anthropic, google, openai-direct).
func NewWithStore(config types.ModelProviderConfig, store *auth.Store) (cogito.LLM, error) {
	return NewWithTemperatureAndStore(config, 0, store)
}

// NewWithTemperatureAndStore is the full constructor: temperature for
// OpenAI-compatible providers, credential store for providers with /login.
// A nil store is valid — the resolver falls through to config/env.
//
// Codex is the only special case (it uses a subprocess, not HTTP). Every
// other provider goes through the registry: the provider Definition is
// looked up, and the registered adapter Factory for its Protocol is called.
func NewWithTemperatureAndStore(config types.ModelProviderConfig, temperature float32, store *auth.Store) (cogito.LLM, error) {
	if normalize(config.Provider) == ProviderCodex {
		return codexapp.New(codexapp.Config{
			Command: config.Command,
			Args:    config.Args,
			Model:   config.Model,
		}), nil
	}

	def, ok := provider.Get(normalize(config.Provider))
	if !ok {
		return nil, fmt.Errorf("unknown LLM provider %q — see 'nib login --list' for available providers", config.Provider)
	}

	factory, ok := registry.Get(def.Protocol)
	if !ok {
		return nil, fmt.Errorf("provider %q: no adapter registered for protocol %q", config.Provider, def.Protocol)
	}

	return factory(def, config, store, temperature)
}

func normalize(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "openai", "openai-compatible", "openai_compatible":
		return ProviderOpenAI
	case "codex", "codex-app-server", "codex_app_server":
		return ProviderCodex
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}

func IsCodex(config types.ModelProviderConfig) bool {
	return normalize(config.Provider) == ProviderCodex
}

func orDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
