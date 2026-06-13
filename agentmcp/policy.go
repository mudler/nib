package agentmcp

import (
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

// policy decides tool-call approval for a hands-free session. With no
// human at a terminal it never blocks: it approves, or (allowlist mode) denies
// without prompting. A denial surfaces to the model as a normal denied-tool
// result, which it explains in its next (spoken) reply.
type policy struct {
	mode       string
	allowed    map[string]bool
	askDefault string
}

func newPolicy(cfg types.Config) policy {
	mode := cfg.ApprovalMode
	if mode == "" || mode == "prompt" {
		mode = "auto" // no terminal to prompt at
	}
	allowed := make(map[string]bool, len(cfg.AllowedTools))
	for _, t := range cfg.AllowedTools {
		allowed[t] = true
	}
	return policy{mode: mode, allowed: allowed, askDefault: "Please proceed."}
}

func (p policy) decide(req chat.ToolCallRequest) chat.ToolCallResponse {
	if p.mode == "allowlist" && !p.allowed[req.Name] {
		return chat.ToolCallResponse{Approved: false}
	}
	return chat.ToolCallResponse{Approved: true}
}
