package agentmcp

import (
	"testing"

	"github.com/mudler/nib/chat"
)

// Compile-time guard: *chat.Session must satisfy the session interface the
// MCP server depends on. Signature drift fails here, not at runtime.
func TestChatSessionSatisfiesSessionInterface(t *testing.T) {
	var _ session = (*chat.Session)(nil)
}
