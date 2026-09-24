package mcp

import (
	"sync"

	"github.com/mudler/nib/types"
)

// OutputLimitsPolicy is a thread-safe wrapper around the tool-output limits
// config. The MCP tool handlers read it on every call; the session updates it
// when /settings changes a tool_output_limits.* key at runtime.
//
// It is shared between the bash/filesystem MCP servers (started in
// StartTransports, running as goroutines) and the Session (which owns the
// setter). Both sides go through this struct rather than reading the Config
// directly, so a /settings update lands without a restart.
type OutputLimitsPolicy struct {
	mu     sync.RWMutex
	limits types.ToolOutputLimitsConfig
}

// NewOutputLimitsPolicy creates a policy seeded from cfg.
func NewOutputLimitsPolicy(cfg types.ToolOutputLimitsConfig) *OutputLimitsPolicy {
	return &OutputLimitsPolicy{limits: cfg}
}

// Get returns a snapshot of the current limits.
func (p *OutputLimitsPolicy) Get() types.ToolOutputLimitsConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.limits
}

// Set replaces the limits. Called from Session.SetToolOutputLimits.
func (p *OutputLimitsPolicy) Set(l types.ToolOutputLimitsConfig) {
	p.mu.Lock()
	p.limits = l
	p.mu.Unlock()
}

// Resolved returns the current limits with defaults filled in. This is what
// tool handlers should call on each invocation.
func (p *OutputLimitsPolicy) Resolved() OutputLimits {
	return ResolveOutputLimits(p.Get())
}
