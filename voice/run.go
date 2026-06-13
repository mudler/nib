package voice

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

// Run builds a headless voice session and serves it as an MCP server over the
// transport selected by opts. transports are the agent's tool servers (shell,
// filesystem, web, plugins) exactly as the TUI/CLI receive them.
func Run(ctx context.Context, cfg types.Config, opts Options, transports ...mcp.Transport) error {
	cfg = applyProfile(cfg)
	r := newRouter()
	pol := newPolicy(cfg)

	sess, err := chat.NewSession(ctx, cfg, buildCallbacks(r, pol), transports...)
	if err != nil {
		return err
	}

	srv := newServer(ctx, sess, r)
	return serve(ctx, srv, opts)
}
