package catalog

import "testing"

func TestLookupContextRoute(t *testing.T) {
	for _, tc := range []struct {
		provider, model, url string
		want                 int
	}{
		{"openai", "gpt-6.1-sol", "", 1050000},
		{"openai-codex", "gpt-6.1-sol", "", 500000},
		{"anthropic-api-key", "claude-opus-4-6", "", 1000000},
 {"anthropic-api-key", "claude-opus-4-6", "https://api.anthropic.com/proxy", 0},
 {"anthropic-api-key", "claude-opus-4-6", "https://api.anthropic.com.evil", 0},
 {"anthropic-api-key", "claude-opus-4-6", "http://127.0.0.1:1234", 0},
 {"anthropic", "claude-opus-4-6", "https://api.anthropic.com/", 1000000},
		{"openai", "gpt-6.1-sol", "https://api.openai.com/v1/proxy", 0},
		{"openai", "gpt-6.1-sol", "https://api.openai.com.evil/v1", 0},
		{"openai", "gpt-6.1-sol", "https://api.openai.com:444/v1", 0},
		{"openai", "gpt-6.1-sol", "http://127.0.0.1:1234/v1", 0},
		{"azure-openai", "gpt-6.1-sol", "", 0},
		{"anthropic", "gpt-6.1-sol", "", 0},
		{"openai", "GPT-6.1-SOL", "", 0},
		{"openai", "unknown", "", 0},
	} {
		t.Run(tc.provider+tc.model+tc.url, func(t *testing.T) {
			m, ok := LookupContextRoute(tc.provider, tc.model, tc.url)
			if tc.want == 0 {
				if ok {
					t.Fatalf("unexpected match: %+v", m)
				}
				return
			}
			if !ok || m.ContextWindow == nil || *m.ContextWindow != tc.want {
				t.Fatalf("got %+v, want %d", m, tc.want)
			}
		})
	}
}

func TestAnthropicAPIKeyContextCanonicalIdentity(t *testing.T) {
 for _, base := range []string{"", "https://api.anthropic.com", "https://api.anthropic.com/"} {
  id, root, ok := ContextRoute("anthropic-api-key", base)
  if !ok || id != "anthropic" || root != "https://api.anthropic.com" { t.Fatalf("route = %s %s %v", id, root, ok) }
 }
}
