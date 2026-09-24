package llmprovider

import (
	"testing"

	"github.com/mudler/nib/types"
)

func TestClampEffort(t *testing.T) {
	supported := []string{"low", "medium", "high"}
	tests := []struct{ effort, want string }{
		{"max", "high"},
		{"xhigh", "high"},
		{"high", "high"},
		{"medium", "medium"},
		{"low", "low"},
	}
	for _, tc := range tests {
		got := ClampEffort(tc.effort, supported)
		if got != tc.want {
			t.Errorf("ClampEffort(%q, %v) = %q, want %q", tc.effort, supported, got, tc.want)
		}
	}
}

func TestClampEffortEmpty(t *testing.T) {
	if got := ClampEffort("max", nil); got != "max" {
		t.Errorf("ClampEffort with nil should return effort unchanged, got %q", got)
	}
}

func TestResolveReasoningEmpty(t *testing.T) {
	cfg := types.Config{}
	provider := types.ModelProviderConfig{}
	got := ResolveReasoning(cfg, provider, "")
	if got.Effort != "" {
		t.Errorf("expected empty effort, got %q", got.Effort)
	}
}

func TestResolveReasoningOverride(t *testing.T) {
	cfg := types.Config{
		ReasoningOverrides: map[string]string{"gpt-5": "xhigh"},
	}
	provider := types.ModelProviderConfig{
		Model:           "gpt-5",
		ReasoningEffort: "high",
	}
	got := ResolveReasoning(cfg, provider, "")
	if got.Effort != "xhigh" {
		t.Errorf("expected xhigh from override, got %q", got.Effort)
	}
}

func TestResolveReasoningBudgetMode(t *testing.T) {
	cfg := types.Config{
		ThinkingMode: "budget",
	}
	provider := types.ModelProviderConfig{
		Model:           "test-model",
		ReasoningEffort: "xhigh",
	}
	got := ResolveReasoning(cfg, provider, "")
	if got.Mode != "budget" {
		t.Errorf("expected budget mode, got %q", got.Mode)
	}
	if got.BudgetTokens != 32768 {
		t.Errorf("expected 32768 budget tokens, got %d", got.BudgetTokens)
	}
}
