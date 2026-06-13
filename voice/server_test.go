package voice

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

// decodeStructured re-marshals a tool result's StructuredContent (which arrives
// on the client as a generic JSON value) into a typed output struct. The SDK
// exposes StructuredContent as a field of type any, not a decode method.
func decodeStructured(t *testing.T, res *mcp.CallToolResult, out any) {
	t.Helper()
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
}

// fakeSession simulates chat.Session: SendMessage drives the callbacks the way
// a real run would (optionally parking before the final reply).
type fakeSession struct {
	cb          chat.Callbacks
	parkFirst   bool
	reply       string
	live        bool
	injectFails bool // InjectUser returns false without driving a reply
	interrupt   int
}

func (f *fakeSession) SendMessage(text string) (string, error) {
	if f.parkFirst {
		f.cb.OnParked("working on it")
	}
	f.cb.OnResponse(f.reply)
	return f.reply, nil
}
func (f *fakeSession) InjectUser(string) bool {
	if f.injectFails {
		return false
	}
	f.cb.OnResponse(f.reply)
	return true
}
func (f *fakeSession) RunLive() bool             { return f.live }
func (f *fakeSession) Interrupt()                { f.interrupt++ }
func (f *fakeSession) TakeUndelivered() []string { return nil }

func dialServer(t *testing.T, sess session, r *router) *mcp.ClientSession {
	t.Helper()
	srvT, cliT := mcp.NewInMemoryTransports()
	srv := newServer(context.Background(), sess, r)
	go func() { _ = srv.Run(context.Background(), srvT) }()

	says := make(chan sayPayload, 8)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, &mcp.ClientOptions{
		LoggingMessageHandler: func(_ context.Context, req *mcp.LoggingMessageRequest) {
			b, _ := req.Params.Data.(map[string]any) // Data round-trips as JSON object
			if b != nil {
				says <- sayPayload{
					Kind: asString(b["kind"]), Text: asString(b["text"]),
					Message: asString(b["message"]),
				}
			}
		},
	})
	cs, err := client.Connect(context.Background(), cliT, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	// The server drops logging notifications until the client sets a level, so
	// subscribe to info-and-above before any converse runs.
	if err := cs.SetLoggingLevel(context.Background(), &mcp.SetLoggingLevelParams{Level: "info"}); err != nil {
		t.Fatalf("set logging level: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	saysByServer[r] = says
	return cs
}

// saysByServer lets a test fetch the notification channel for its router.
var saysByServer = map[*router]chan sayPayload{}

func asString(v any) string { s, _ := v.(string); return s }

func TestConverseReturnsParkReplyThenNotifies(t *testing.T) {
	r := newRouter()
	sess := &fakeSession{parkFirst: true, reply: "all done"}
	sess.cb = buildCallbacks(r, newPolicy(types.Config{}))
	cs := dialServer(t, sess, r)

	var out converseOut
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "converse",
		Arguments: map[string]any{"utterance": "build the thing"},
	})
	if err != nil {
		t.Fatalf("converse: %v", err)
	}
	decodeStructured(t, res, &out)
	if out.Reply != "working on it" || !out.Pending {
		t.Fatalf("converse out = %+v, want {working on it, pending}", out)
	}

	select {
	case s := <-saysByServer[r]:
		if s.Kind != "say" || s.Text != "all done" {
			t.Fatalf("notification = %+v, want say 'all done'", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no nib/say notification for the final reply")
	}
}

func TestConverseSimpleTurn(t *testing.T) {
	r := newRouter()
	sess := &fakeSession{reply: "hi there"}
	sess.cb = buildCallbacks(r, newPolicy(types.Config{}))
	cs := dialServer(t, sess, r)

	var out converseOut
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "converse", Arguments: map[string]any{"utterance": "hello"},
	})
	if err != nil {
		t.Fatalf("converse: %v", err)
	}
	decodeStructured(t, res, &out)
	if out.Reply != "hi there" || out.Pending {
		t.Fatalf("converse out = %+v, want {hi there, not pending}", out)
	}
}

// When a run is live but InjectUser finds no live run to inject into (it ended
// in the race window, or the inject channel was full), converse must fall back
// to a fresh SendMessage turn rather than hang waiting for a reply that never
// comes.
func TestConverseFallsBackWhenInjectFindsNoLiveRun(t *testing.T) {
	r := newRouter()
	sess := &fakeSession{reply: "fell back", live: true, injectFails: true}
	sess.cb = buildCallbacks(r, newPolicy(types.Config{}))
	cs := dialServer(t, sess, r)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "converse", Arguments: map[string]any{"utterance": "hello"},
	})
	if err != nil {
		t.Fatalf("converse: %v", err)
	}
	var out converseOut
	decodeStructured(t, res, &out)
	if out.Reply != "fell back" {
		t.Fatalf("converse out = %+v, want reply 'fell back' (fell back to SendMessage)", out)
	}
}

func TestInterruptCallsSession(t *testing.T) {
	r := newRouter()
	sess := &fakeSession{reply: "x"}
	sess.cb = buildCallbacks(r, newPolicy(types.Config{}))
	cs := dialServer(t, sess, r)
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "interrupt"}); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	if sess.interrupt == 0 {
		t.Fatal("interrupt tool did not reach the session")
	}
}
