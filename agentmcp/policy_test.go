package agentmcp

import (
	"testing"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

func TestPolicyAutoApprovesByDefault(t *testing.T) {
	pol := newPolicy(types.Config{}) // empty ApprovalMode -> auto
	resp := pol.decide(chat.ToolCallRequest{Name: "execute_command"})
	if !resp.Approved {
		t.Fatal("default policy must approve (hands-free, no terminal)")
	}
}

func TestPolicyAllowlistDeniesUnlisted(t *testing.T) {
	pol := newPolicy(types.Config{ApprovalMode: "allowlist", AllowedTools: []string{"read_file"}})
	if !pol.decide(chat.ToolCallRequest{Name: "read_file"}).Approved {
		t.Fatal("allowlisted tool must be approved")
	}
	if pol.decide(chat.ToolCallRequest{Name: "execute_command"}).Approved {
		t.Fatal("unlisted tool must be denied in allowlist mode")
	}
}

func TestPolicyAskDefaultNonEmpty(t *testing.T) {
	if newPolicy(types.Config{}).askDefault == "" {
		t.Fatal("askDefault must be non-empty so the agent never hangs")
	}
}
