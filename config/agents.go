package config

import "github.com/mudler/nib/types"

// AgentTypeConfig is re-exported for ergonomic use within the config package.
type AgentTypeConfig = types.AgentTypeConfig

// defaultAgentTypes are the built-in sub-agent personas. They are overridable
// and extendable via the `agents:` block in wiz config (see MergeAgentTypes).
func defaultAgentTypes() []AgentTypeConfig {
	return []AgentTypeConfig{
		{
			Name:         "general",
			Description:  "general-purpose helper for self-contained subtasks",
			SystemPrompt: "You are a focused sub-agent. Complete the given task and report a concise result.",
			Iterations:   15,
		},
		{
			Name:         "explore",
			Description:  "read-only codebase/file exploration; returns findings",
			SystemPrompt: "You are an exploration sub-agent. Investigate and summarize findings. Prefer read-only tools. Return the conclusion, not raw dumps.",
			Iterations:   25,
		},
		{
			Name:         "plan",
			Description:  "produce a step-by-step plan for a goal without executing it",
			SystemPrompt: "You are a planning sub-agent. Produce a concrete, ordered plan. Do not execute irreversible actions.",
			Iterations:   15,
		},
	}
}

// MergeAgentTypes returns the built-in types with any user entries merged in:
// an entry whose Name matches a default overrides it field-for-field; a new
// Name is appended. Defaults not mentioned by the user are preserved.
func MergeAgentTypes(user []AgentTypeConfig) []AgentTypeConfig {
	merged := defaultAgentTypes()
	for _, u := range user {
		replaced := false
		for i := range merged {
			if merged[i].Name == u.Name {
				merged[i] = u
				replaced = true
				break
			}
		}
		if !replaced {
			merged = append(merged, u)
		}
	}
	return merged
}
