package voice

import (
	"context"
	"net/http"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// session is the slice of *chat.Session the voice server needs. An interface so
// tests can drive converse/interrupt without a live LLM. *chat.Session
// satisfies it.
type session interface {
	SendMessage(text string) (string, error)
	InjectUser(msg string) bool
	RunLive() bool
	Interrupt()
	TakeUndelivered() []string
}

type converseIn struct {
	Utterance string `json:"utterance" jsonschema:"the transcribed user utterance to send to the agent"`
}
type converseOut struct {
	Reply   string `json:"reply" jsonschema:"the agent's spoken reply"`
	Pending bool   `json:"pending" jsonschema:"true when background work continues after this reply"`
	Turn    int    `json:"turn" jsonschema:"turn id; later nib/say notifications carry the same id"`
}
type interruptIn struct{}
type interruptOut struct{}

// sayPayload is the JSON Data of a nib/say (or nib/error) logging notification.
type sayPayload struct {
	Kind    string `json:"kind"` // "say" | "error"
	Text    string `json:"text,omitempty"`
	Message string `json:"message,omitempty"`
	Pending bool   `json:"pending,omitempty"`
	Turn    int    `json:"turn"`
}

// Options selects the transport for the voice MCP server.
type Options struct {
	HTTP bool   // serve over streamable HTTP instead of stdio
	Addr string // HTTP listen address (with HTTP=true)
}

// newServer builds the voice MCP server. runCtx must outlive individual tool
// calls: it backs the proactive notification sink, which fires after converse
// returns.
func newServer(runCtx context.Context, sess session, r *router) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "nib", Version: "v1.0.0"}, nil)

	var bindOnce sync.Once
	bind := func(req *mcp.CallToolRequest) {
		bindOnce.Do(func() { r.setNotify(notifier(runCtx, req.Session)) })
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "converse",
		Description: "Send a transcribed user utterance to nib's agent and get the first spoken reply. " +
			"Returns when the agent first replies (possibly while background work continues, pending=true); " +
			"later replies arrive as nib/say logging notifications carrying the same turn id.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in converseIn) (*mcp.CallToolResult, converseOut, error) {
		bind(req)
		ch, turn := r.await()
		if sess.RunLive() {
			sess.InjectUser(in.Utterance)
		} else {
			go func() {
				if _, err := sess.SendMessage(in.Utterance); err != nil {
					r.emit(replyEvent{Err: err})
				}
				// Re-dispatch follow-ups that the run ended before consuming, so
				// a quickly-spoken utterance is never silently dropped.
				for _, u := range sess.TakeUndelivered() {
					_, _ = sess.SendMessage(u)
				}
			}()
		}
		select {
		case ev := <-ch:
			if ev.Err != nil {
				return nil, converseOut{Turn: turn}, ev.Err
			}
			return nil, converseOut{Reply: ev.Text, Pending: ev.Pending, Turn: turn}, nil
		case <-ctx.Done():
			return nil, converseOut{Turn: turn}, ctx.Err()
		}
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "interrupt",
		Description: "Cancel the agent's current turn (barge-in). No-op when nothing is running.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ interruptIn) (*mcp.CallToolResult, interruptOut, error) {
		sess.Interrupt()
		return nil, interruptOut{}, nil
	})

	return srv
}

// notifier returns a notifyFunc that pushes nib/say (or nib/error) as a logging
// notification on the client session, using the long-lived run context.
func notifier(ctx context.Context, ss *mcp.ServerSession) notifyFunc {
	return func(ev replyEvent, turn int) {
		level := mcp.LoggingLevel("info")
		var p sayPayload
		if ev.Err != nil {
			p = sayPayload{Kind: "error", Message: ev.Err.Error(), Turn: turn}
			level = "error"
		} else {
			p = sayPayload{Kind: "say", Text: ev.Text, Pending: ev.Pending, Turn: turn}
		}
		_ = ss.Log(ctx, &mcp.LoggingMessageParams{Logger: "nib", Level: level, Data: p})
	}
}

// serve runs srv over the transport chosen by opts, until ctx is cancelled.
func serve(ctx context.Context, srv *mcp.Server, opts Options) error {
	if opts.HTTP {
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
		httpSrv := &http.Server{Addr: opts.Addr, Handler: handler}
		go func() {
			<-ctx.Done()
			_ = httpSrv.Close()
		}()
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
	return srv.Run(ctx, &mcp.StdioTransport{})
}
