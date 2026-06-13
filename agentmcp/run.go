package agentmcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/chat"
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/types"
)

// Run builds a headless agent session and serves it as an MCP server over the
// transport selected by opts. transports are the agent's tool servers (shell,
// filesystem, web, plugins) exactly as the TUI/CLI receive them. shellJobs is
// the shared background-shell registry (the same one StartTransports gave the
// bash server); wiring it lets `bash_background` jobs keep a converse run parked
// and push their completion as a nib/reply notification. It may be nil.
func Run(ctx context.Context, cfg types.Config, opts Options, shellJobs *wizmcp.ShellJobs, transports ...mcp.Transport) error {
	r := newRouter()
	pol := newPolicy(cfg)

	sess, err := chat.NewSession(ctx, cfg, buildCallbacks(r, pol), transports...)
	if err != nil {
		return err
	}
	if shellJobs != nil {
		// Without this, the pending-work predicate never sees background shell
		// jobs, so they neither park the run nor inject a completion notice.
		sess.SetShellJobs(shellJobs)
	}

	srv := newServer(ctx, sess, r)
	return serve(ctx, srv, opts)
}
