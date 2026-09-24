package llmprovider

import (
	"strings"

	"github.com/mudler/nib/llmprovider/catalog"
	"github.com/mudler/nib/types"
)

type ResolvedReasoning struct {
	Effort       string
	Mode         string
	BudgetTokens int
}

func ResolveReasoning(cfg types.Config, provider types.ModelProviderConfig, baseURL string) ResolvedReasoning {
	effort := provider.ReasoningEffort
	if ov, ok := cfg.ReasoningOverrides[provider.Model]; ok && ov != "" {
		effort = ov
	}
	if effort == "" {
		return ResolvedReasoning{}
	}
	mode := provider.ThinkingMode
	if mode == "" {
		mode = cfg.ThinkingMode
	}
	if mode == "" {
		mode = types.DefaultThinkingMode
	}
	if m, ok := catalog.Lookup(provider.Provider, provider.Model, baseURL); ok && len(m.Compat.SupportedReasoningEfforts) > 0 {
		effort = ClampEffort(effort, m.Compat.SupportedReasoningEfforts)
	}
	out := ResolvedReasoning{Effort: effort, Mode: mode}
	if mode == "budget" {
		budgets := cfg.ThinkingBudgets
		if len(budgets) == 0 {
			budgets = types.DefaultThinkingBudgets
		}
		if n, ok := budgets[strings.ToLower(effort)]; ok && n > 0 {
			out.BudgetTokens = n
		}
	}
	return out
}

func ClampEffort(effort string, supported []string) string {
	if len(supported) == 0 {
		return effort
	}
	supSet := make(map[string]bool, len(supported))
	for _, s := range supported {
		supSet[strings.ToLower(strings.TrimSpace(s))] = true
	}
	if supSet[strings.ToLower(effort)] {
		return effort
	}
	effIdx, effKnown := indexOf(types.EffortOrder, strings.ToLower(effort))
	if !effKnown {
		return effort
	}
	for i := effIdx; i >= 0; i-- {
		if supSet[types.EffortOrder[i]] {
			return types.EffortOrder[i]
		}
	}
	return effort
}

func indexOf(xs []string, s string) (int, bool) {
	for i, x := range xs {
		if x == s {
			return i, true
		}
	}
	return -1, false
}
