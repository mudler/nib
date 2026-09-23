package types

import "strings"

// ApprovalMode is approval_mode: how tool calls are gated.
type ApprovalMode string

const (
	// ApprovalPrompt asks the user, but approves read-only calls. The empty
	// mode means the same.
	ApprovalPrompt ApprovalMode = "prompt"
	// ApprovalStrict asks the user for every call.
	ApprovalStrict ApprovalMode = "strict"
	// ApprovalAllowlist approves only the tools in allowed_tools and asks
	// for the rest.
	ApprovalAllowlist ApprovalMode = "allowlist"
	// ApprovalClassify is ApprovalPrompt with a classifier in front of the
	// prompt, which may approve the call first.
	ApprovalClassify ApprovalMode = "classify"
	// ApprovalAuto approves every call.
	ApprovalAuto ApprovalMode = "auto"
)

// ApprovalModes are the valid modes, in the order they are offered.
var ApprovalModes = []ApprovalMode{ApprovalPrompt, ApprovalStrict, ApprovalAllowlist, ApprovalClassify, ApprovalAuto}

// Valid reports whether m is one of ApprovalModes.
func (m ApprovalMode) Valid() bool {
	for _, v := range ApprovalModes {
		if m == v {
			return true
		}
	}
	return false
}

// OrDefault is m, or ApprovalPrompt for the empty mode.
func (m ApprovalMode) OrDefault() ApprovalMode {
	if m == "" {
		return ApprovalPrompt
	}
	return m
}

// ParseApprovalMode reads a mode as a user types it: any case, blank
// meaning prompt. ok is false for anything else.
func ParseApprovalMode(s string) (ApprovalMode, bool) {
	m := ApprovalMode(strings.ToLower(strings.TrimSpace(s))).OrDefault()
	return m, m.Valid()
}

// ApprovalModeNames are ApprovalModes as strings, for completion and help.
func ApprovalModeNames() []string {
	out := make([]string, len(ApprovalModes))
	for i, m := range ApprovalModes {
		out[i] = string(m)
	}
	return out
}
