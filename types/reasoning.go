package types

var EffortOrder = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

var DefaultThinkingBudgets = map[string]int{
	"minimal": 1024,
	"low":     4096,
	"medium":  8192,
	"high":    16384,
	"xhigh":   32768,
	"max":     65536,
}

const DefaultThinkingMode = "effort"
