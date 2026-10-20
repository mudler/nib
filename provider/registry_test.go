package provider

import "testing"

func TestAnthropicAPIKeyIdentity(t *testing.T) {
	d, ok := Get("anthropic-api-key")
	if !ok {
		t.Fatal("missing anthropic-api-key provider")
	}
	if d.ID != "anthropic-api-key" || d.Name != "Anthropic (API key)" || d.Protocol != ProtocolAnthropicMessages || d.BaseURL != "https://api.anthropic.com" || d.EnvVar != "ANTHROPIC_API_KEY" || d.LoginKind != LoginAPIKey {
		t.Fatalf("incorrect definition: %+v", d)
	}
	if d.ClientID != "" || d.TokenURL != "" || d.AuthorizeURL != "" || len(d.ExtraTokenHeaders) != 0 {
		t.Fatal("API-key entry has OAuth settings")
	}
	oauth, _ := Get("anthropic")
	if oauth.LoginKind != LoginOAuthCode || oauth.TokenURL == "" {
		t.Fatal("existing OAuth identity changed")
	}
	for _, list := range [][]Definition{All(), Loginable()} {
		counts := map[string]int{}
		for _, def := range list {
			counts[def.ID]++
		}
		if counts[d.ID] != 1 || counts[oauth.ID] != 1 {
			t.Fatal("identities must appear independently exactly once")
		}
	}
}
