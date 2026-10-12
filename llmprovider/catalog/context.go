package catalog

import "strings"

// ContextRoute recognizes only the service roots whose context metadata we
// consume. Deployment names and custom proxies are not vendor model identities.
func ContextRoute(provider, baseURL string) (string, string, bool) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" || provider == "openai-compatible" || provider == "openai_compatible" {
		provider = "openai"
	}
	var root string
	switch provider {
	case "openai":
		root = "https://api.openai.com/v1"
	case "anthropic":
		root = "https://api.anthropic.com"
	case "openai-codex":
		root = "https://chatgpt.com/backend-api"
	case "google":
		root = "https://generativelanguage.googleapis.com/v1beta"
	default:
		return "", "", false
	}
	actual := strings.TrimRight(baseURL, "/")
	if actual == "" {
		actual = root
	}
	if provider == "openai" && actual == "https://api.openai.com" {
		actual = root
	}
	return provider, root, actual == root
}

// LookupContextRoute uses exact provider, model and service identity. Unlike
// Lookup it never searches other providers, suffixes or case-folded model IDs.
func LookupContextRoute(provider, model, baseURL string) (*Model, bool) {
	provider, root, ok := ContextRoute(provider, baseURL)
	if !ok || Load() != nil {
		return nil, false
	}
	m, ok := catalogDB[provider][model]
	if !ok || m.ID != model || m.Provider != provider {
		return nil, false
	}
	_, modelRoot, valid := ContextRoute(provider, m.BaseURL)
	if !valid || modelRoot != root || m.BaseURL == "" {
		return nil, false
	}
	return &m, true
}
