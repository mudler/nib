package selfdoc

import (
	"strings"
	"testing"
)

func TestEmbeddedReadme(t *testing.T) {
	got := EmbeddedReadme()
	if len(got) == 0 {
		t.Fatal("EmbeddedReadme() returned empty string")
	}
	if len(got) < 100 {
		t.Fatalf("EmbeddedReadme() suspiciously short: %d bytes", len(got))
	}
}

func TestEmbeddedReadmeNotZeroValue(t *testing.T) {
	if EmbeddedReadme() == "" {
		t.Fatal("embeddedReadme is empty — go:embed failed or README.md is missing")
	}
}

func TestEmbeddedReadmeContainsKeySections(t *testing.T) {
	got := EmbeddedReadme()
	checks := []string{
		"## Quickstart",
		"## Configuration",
		"## Plugins",
		"## Skills",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("EmbeddedReadme() missing %q", want)
		}
	}
}

func TestStatusDocumentation(t *testing.T) {
	for _, want := range []string{"Working · 2 agents · 1 shell", "Waiting for jobs", "Reviewing results", "Ready for input", "foreground and background child agents", "scheduled eligibility", "availability, not task success", "explicit continuation lines", "Grapheme clusters", "it appears as `?` instead", "Semantic count fields never split"} {
		if !strings.Contains(EmbeddedReadme(), want) {
			t.Errorf("status documentation missing %q", want)
		}
	}
}

// The built-in about-nib skill must distinguish subscription and API access.
func TestAnthropicAPIKeyDocumentation(t *testing.T) {
	for _, want := range []string{
		"nib login anthropic-api-key", "Claude Pro/Max", "ANTHROPIC_API_KEY",
		"provider: anthropic-api-key", "model: claude-sonnet-4-5", "api_key: your-api-key",
		"nonstreaming", "incompatible saved credential", "illustrative", "Add Endpoint",
	} {
		if !strings.Contains(EmbeddedReadme(), want) {
			t.Errorf("Anthropic API-key documentation missing %q", want)
		}
	}
}
