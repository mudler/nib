package openairesponses

import (
	"strings"

	"github.com/mudler/nib/llmprovider/catalog"
)

func (l *LLM) summarySupported(model string, explicitEffort bool) bool {
	if l.config.SummarySupport != nil {
		return *l.config.SummarySupport
	}
	if o := l.config.summaryOptions; o != nil {
		if o.Support != nil {
			return *o.Support
		}
		if m, ok := catalog.LookupResponsesRoute(o.Provider, model, o.BaseURL); ok && m.Compat.SupportsReasoningSummary != nil {
			return *m.Compat.SupportsReasoningSummary && !legacySummaryExcluded(model)
		}
		return false
	}
	if l.config.BaseURL != "" {
		m, ok := catalog.LookupResponsesRoute("openai", model, l.config.BaseURL)
		if ok && m.Compat.SupportsReasoningSummary != nil {
			return *m.Compat.SupportsReasoningSummary && !legacySummaryExcluded(model)
		}
		if !catalog.IsOfficialResponsesRoute("openai", l.config.BaseURL) {
			return false
		}
	}
	// Preserve the legacy shared translator (including Azure deployment aliases)
	// and official OpenAI defaults for older models absent from the catalog.
	return supportsReasoningSummary(model, explicitEffort)
}

// Older families have known summary exclusions even where catalog compatibility
// defaults advertise Responses summaries for the whole provider.
func legacySummaryExcluded(model string) bool {
	m := strings.ToLower(model)
	return m == "o1" || strings.HasPrefix(m, "o1-") || strings.HasPrefix(m, "o3-mini") || strings.HasPrefix(m, "gpt-4") || (strings.HasPrefix(m, "gpt-5") && strings.Contains(m, "chat"))
}
