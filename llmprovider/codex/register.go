package codex

import (
	"fmt"
	"strings"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/llmprovider/registry"
	"github.com/mudler/nib/provider"
	"github.com/mudler/nib/types"
)

func init() {
	registry.Register(provider.ProtocolCodexResponses, factory)
}

func factory(def provider.Definition, config types.ModelProviderConfig, store *auth.Store, _ float32) (cogito.LLM, error) {
	resolved, _ := auth.Resolve(store, def, config.APIKey)

	token := resolved.APIKey
	if token == "" {
		return nil, fmt.Errorf("codex: no credentials — run 'nib login %s' or set %s", def.ID, def.EnvVar)
	}

	llm := New(Config{
		Model:           config.Model,
		Token:           token,
		ReasoningEffort: config.ReasoningEffort,
		ServiceTier:     config.Metadata["service_tier"],
		ResponsesLite:   config.Metadata["responses_lite"] == "true" || config.Metadata["responses_lite"] == "1",
	})
	llm.catalogSuggestions = def.ID == "openai-codex" && (config.BaseURL == "" || strings.TrimRight(config.BaseURL, "/") == codexBaseURL)
	return llm, nil
}
