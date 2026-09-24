package chat

import (
	"context"
	"io"
	"net/http"
	"sync"

	"github.com/mudler/xlog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpConnectTimeout = 10 * time.Second

// connectMCP bounds the handshake without imposing a deadline on the live
// connection. The SDK retains this context for its background SSE listener.
func connectMCP(ctx context.Context, client *mcp.Client, transport mcp.Transport, timeout time.Duration) (*mcp.ClientSession, error) {
	connectionCtx, cancel := context.WithCancel(ctx)
	// The SDK sends cancellation notifications with a detached context. Bind
	// HTTP requests to the connection lifetime too, or a stalled cancellation
	// POST can keep Connect blocked after its handshake context is cancelled.
	transport = bindMCPTransport(transport, connectionCtx)
	timer := time.AfterFunc(timeout, cancel)
	session, err := client.Connect(connectionCtx, transport, nil)
	stopped := timer.Stop()
	if err != nil || !stopped || connectionCtx.Err() != nil {
		cancel()
		if session != nil {
			_ = session.Close()
		}
		if !stopped && ctx.Err() == nil {
			return nil, context.DeadlineExceeded
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			return nil, err
		}
		return nil, connectionCtx.Err()
	}
	// Do not cancel after success: the connection lives until the parent session
	// ends, or ClientSession.Close closes its transport.
	return session, nil
}

func bindMCPTransport(transport mcp.Transport, ctx context.Context) mcp.Transport {
	bind := func(client *http.Client) *http.Client {
		if client == nil {
			client = http.DefaultClient
		}
		copy := *client
		base := copy.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		copy.Transport = connectionHTTPTransport{base: base, ctx: ctx}
		return &copy
	}
	switch t := transport.(type) {
	case *mcp.InMemoryTransport:
		return cancellableInMemoryTransport{transport: t}
	case *mcp.StreamableClientTransport:
		copy := *t
		copy.HTTPClient = bind(t.HTTPClient)
		return &copy
	case *mcp.SSEClientTransport:
		copy := *t
		copy.HTTPClient = bind(t.HTTPClient)
		return &copy
	default:
		return transport
	}
}

type connectionHTTPTransport struct {
	base http.RoundTripper
	ctx  context.Context
}

func (t connectionHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	stop := context.AfterFunc(t.ctx, cancel)
	cleanup := func() { stop(); cancel() }
	if t.ctx.Err() != nil {
		cleanup()
		return nil, t.ctx.Err()
	}
	resp, err := t.base.RoundTrip(req.Clone(ctx))
	if err != nil {
		cleanup()
		return nil, err
	}
	resp.Body = &connectionHTTPBody{ReadCloser: resp.Body, cleanup: cleanup}
	return resp, nil
}

type connectionHTTPBody struct {
	io.ReadCloser
	cleanup func()
}

func (b *connectionHTTPBody) Close() error {
	defer b.cleanup()
	return b.ReadCloser.Close()
}

// In-memory transports use net.Pipe: a write to a server that never starts
// blocks even when the request context is cancelled. Closing the connection
// on lifetime cancellation releases both reads and writes.
type cancellableInMemoryTransport struct{ transport *mcp.InMemoryTransport }

func (t cancellableInMemoryTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.transport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	return &cancellableInMemoryConnection{Connection: conn, stop: stop}, nil
}

type cancellableInMemoryConnection struct {
	mcp.Connection
	stop func() bool
}

func (c *cancellableInMemoryConnection) Close() error {
	c.stop()
	return c.Connection.Close()
}

const mcpConnectConcurrency = 10

type mcpConnectJob struct {
	onSlow    func(string)
	name      string
	transport mcp.Transport
}

type mcpConnectResult struct {
	session *mcp.ClientSession
	err     error
}

// connectMCPBatch keeps connection attempts bounded and results in input order.
// Only workers touch their result slots; callers update session maps afterward.
func connectMCPBatch(ctx context.Context, client *mcp.Client, jobs []mcpConnectJob) []mcpConnectResult {
	results := make([]mcpConnectResult, len(jobs))
	pending := make(chan int, len(jobs))
	for i := range jobs {
		pending <- i
	}
	close(pending)
	var workers sync.WaitGroup
	for range min(mcpConnectConcurrency, len(jobs)) {
		workers.Go(func() {
			for i := range pending {
				if err := ctx.Err(); err != nil {
					results[i].err = err
					continue
				}
				results[i].session, results[i].err = connectNamedMCP(ctx, client, jobs[i])
			}
		})
	}
	workers.Wait()
	return results
}

func connectNamedMCP(ctx context.Context, client *mcp.Client, job mcpConnectJob) (*mcp.ClientSession, error) {
	xlog.Debug("Connecting MCP server", "name", job.name)
	started := time.Now()
	slow := time.AfterFunc(time.Second, func() {
		xlog.Warn("MCP server is taking a long time to connect", "name", job.name, "elapsed", time.Since(started))
		if job.onSlow != nil {
			job.onSlow(job.name)
		}
	})
	defer slow.Stop()
	session, err := connectMCP(ctx, client, job.transport, mcpConnectTimeout)
	if err == nil {
		xlog.Debug("MCP server connected", "name", job.name, "elapsed", time.Since(started))
	}
	return session, err
}
