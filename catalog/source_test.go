package catalog

import "testing"

func TestDetectSourceKind(t *testing.T) {
	cases := []struct {
		url       string
		wantKind  string
		wantLabel string
	}{
		{"https://acme.dev/catalog/index.json", SourceIndex, "acme.dev"},
		{"https://github.com/openclaw/agent-skills", SourceGitHub, "openclaw/agent-skills"},
		{"git@github.com:openclaw/agent-skills.git", SourceGitHub, "openclaw/agent-skills"},
		{"acme.dev", SourceWellKnown, "acme.dev"},
		{"https://acme.dev/", SourceWellKnown, "acme.dev"},
	}
	for _, tc := range cases {
		k, l := DetectSourceKind(tc.url)
		if k != tc.wantKind || l != tc.wantLabel {
			t.Errorf("DetectSourceKind(%q) = (%q,%q), want (%q,%q)", tc.url, k, l, tc.wantKind, tc.wantLabel)
		}
	}
}

func TestParseGitHubRepo(t *testing.T) {
	cases := map[string][2]string{
		"https://github.com/openclaw/agent-skills":     {"openclaw", "agent-skills"},
		"https://github.com/openclaw/agent-skills.git": {"openclaw", "agent-skills"},
		"https://github.com/openclaw/agent-skills/":    {"openclaw", "agent-skills"},
		"git@github.com:openclaw/agent-skills.git":     {"openclaw", "agent-skills"},
	}
	for in, want := range cases {
		o, r, ok := parseGitHubRepo(in)
		if !ok || o != want[0] || r != want[1] {
			t.Errorf("parseGitHubRepo(%q) = (%q,%q,%v)", in, o, r, ok)
		}
	}
	if _, _, ok := parseGitHubRepo("https://gitlab.com/x/y"); ok {
		t.Error("non-github URL should not parse")
	}
}
