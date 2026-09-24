package chat

import (
	"context"
	"io"
	"net/http"
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
	transport = bindHTTPTransport(transport, connectionCtx)
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
		if err != nil {
			return nil, err
		}
		return nil, connectionCtx.Err()
	}
	// Do not cancel after success: the connection lives until the parent session
	// ends, or ClientSession.Close closes its transport.
	return session, nil
}

func bindHTTPTransport(transport mcp.Transport, ctx context.Context) mcp.Transport {
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
