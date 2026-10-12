package catalog

import "strings"

// IsOfficialResponsesRoute recognizes only known Responses service roots.
// Custom proxies, paths, ports and lookalike hosts never inherit vendor metadata.
func IsOfficialResponsesRoute(provider, baseURL string) bool {
	root := strings.TrimRight(baseURL, "/")
	switch provider {
	case "openai":
		return root == "https://api.openai.com" || root == "https://api.openai.com/v1"
	case "github-copilot":
		return root == "https://api.githubcopilot.com" || root == "https://api.business.githubcopilot.com" || root == "https://api.enterprise.githubcopilot.com"
	}
	return false
}

// LookupResponsesRoute has no cross-provider, case-folded or suffix fallback.
func LookupResponsesRoute(provider, model, baseURL string) (*Model, bool) {
	if !IsOfficialResponsesRoute(provider, baseURL) || Load() != nil {
		return nil, false
	}
	m, ok := catalogDB[provider][model]
	if !ok || m.ID != model || m.Provider != provider || m.API != "openai-responses" || !IsOfficialResponsesRoute(provider, m.BaseURL) {
		return nil, false
	}
	return &m, true
}
