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
//
// A cfg.TraceDir that cannot be opened fails the call outright: this path has
// no console to warn on, so serving untraced would hide the problem until
// someone went looking for a transcript that was never written.
func Run(ctx context.Context, cfg types.Config, opts Options, shellJobs *wizmcp.ShellJobs, transports ...mcp.Transport) error {
	r := newRouter()
	pol := newPolicy(cfg)

	sess, err := chat.NewSession(ctx, cfg, buildCallbacks(r, pol), transports...)
	if err != nil {
		return err
	}
	// serve blocks for the whole lifetime of the server on both branches (stdio
	// runs until the client hangs up or ctx ends; HTTP until ctx closes the
	// listener), so this defer fires when serving is genuinely over and never
	// mid-session. Without it this path leaked the tool clients and the trace
	// recorder, and — since usage.json is written by Close — was the one entry
	// point that accepted a TraceDir but could never leave a spend report.
	defer sess.Close()
	if shellJobs != nil {
		// Without this, the pending-work predicate never sees background shell
		// jobs, so they neither park the run nor inject a completion notice.
		sess.SetShellJobs(shellJobs)
	}

	srv := newServer(ctx, sess, r)
	return serve(ctx, srv, opts)
}
