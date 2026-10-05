package agentmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGoalPausedCallbackDoesNotConsumeWaiter(t *testing.T) {
	for _, late := range []bool{false, true} {
		r := newRouter()
		cb := buildCallbacks(r, newPolicy(types.Config{}))
		if cb.OnGoalPaused == nil {
			t.Fatal("missing durable pause callback")
		}
		notices := 0
		r.setNotify(func(ev replyEvent, turn int) {
			notices++
			if turn != 1 {
				t.Errorf("turn=%d", turn)
			}
		})
		ch, _ := r.await()
		if late {
			cb.OnResponse("done")
		}
		cb.OnGoalPaused(chat.GoalPausedNotice{MaxReprompts: 1, Window: time.Hour, Paused: true})
		if !late {
			select {
			case ev := <-ch:
				t.Fatalf("pause consumed waiter: %+v", ev)
			default:
			}
			cb.OnResponse("done")
		}
		if ev := <-ch; ev.Text != "done" {
			t.Fatalf("reply=%+v", ev)
		}
		cb.OnResponse("done") // existing duplicate suppression must survive the notice
		if notices != 1 {
			t.Fatalf("notices=%d", notices)
		}
	}
}

// Both streamable HTTP and the bidirectional transport used by stdio share
// the same router and logging protocol. Exercise real active/restored sessions.
func TestGoalNoticeConverseTransports(t *testing.T) {
	for _, httpMode := range []bool{false, true} {
		for _, paused := range []bool{false, true} {
			t.Run(fmt.Sprintf("http=%v/paused=%v", httpMode, paused), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				llm := fakeLLM(t, "still working")
				defer llm.Close()
				cfg := types.Config{BaseDir: t.TempDir(), BaseURL: llm.URL + "/v1", Model: "fake", APIKey: "fake", InitialGoal: "unfinished", InitialGoalPaused: paused, Goal: types.GoalConfig{MaxReprompts: 1, RepromptWindow: "1h"}}
				cfg.AgentOptions = types.AgentOptions{Iterations: 10, MaxAttempts: 1, MaxRetries: 1}
				r := newRouter()
				sess, err := chat.NewSession(ctx, cfg, buildCallbacks(r, newPolicy(cfg)))
				if err != nil {
					t.Fatal(err)
				}
				defer sess.Close()
				srv := newServer(ctx, sess, r)
				var transport mcp.Transport
				if httpMode {
					hs := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil))
					defer hs.Close()
					transport = &mcp.StreamableClientTransport{Endpoint: hs.URL}
				} else {
					st, ct := mcp.NewInMemoryTransports()
					transport = ct
					go func() { _ = srv.Run(ctx, st) }()
				}
				notices := make(chan replyPayload, 8)
				client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, &mcp.ClientOptions{LoggingMessageHandler: func(_ context.Context, req *mcp.LoggingMessageRequest) {
					b, _ := json.Marshal(req.Params.Data)
					var p replyPayload
					_ = json.Unmarshal(b, &p)
					notices <- p
				}})
				cs, err := client.Connect(ctx, transport, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer cs.Close()
				if err := cs.SetLoggingLevel(ctx, &mcp.SetLoggingLevelParams{Level: "info"}); err != nil {
					t.Fatal(err)
				}
				res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "converse", Arguments: map[string]any{"utterance": "hello"}})
				if err != nil {
					t.Fatal(err)
				}
				var out converseOut
				decodeStructured(t, res, &out)
				if out.Reply != "still working" || out.Pending || out.Turn != 1 {
					t.Fatalf("reply=%+v", out)
				}
				if !paused {
					select {
					case p := <-notices:
						if p.Kind != "goal_paused" || p.MaxReprompts != 1 || p.Window != "1h0m0s" || !p.Paused || p.Turn != 1 || strings.Contains(p.Message, "/goal resume") {
							t.Fatalf("notice=%+v", p)
						}
					case <-ctx.Done():
						t.Fatal("missing pause notification")
					}
				}
				if !sess.GoalPaused() || sess.Goal() != "unfinished" {
					t.Fatal("goal state lost")
				}
				select {
				case p := <-notices:
					t.Fatalf("unexpected notification=%+v", p)
				default:
				}
			})
		}
	}
}

type redispatchSession struct {
	fakeSession
	deliveries chan chat.InputDelivery
	taken      bool
}

func (f *redispatchSession) TakeUndelivered() []string {
	if f.taken {
		return nil
	}
	f.taken = true
	return []string{"accepted follow-up"}
}
func (f *redispatchSession) SendMessageWithDelivery(text string, d chat.InputDelivery, parts ...chat.ContentPart) (string, error) {
	f.deliveries <- d
	return "", nil
}
func TestConverseRedispatchInputAccepted(t *testing.T) {
	r := newRouter()
	f := &redispatchSession{fakeSession: fakeSession{reply: "done"}, deliveries: make(chan chat.InputDelivery, 1)}
	f.cb = buildCallbacks(r, newPolicy(types.Config{}))
	cs := dialServer(t, f, r)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "converse", Arguments: map[string]any{"utterance": "fresh"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case d := <-f.deliveries:
		if d != chat.InputAccepted {
			t.Fatalf("delivery=%v", d)
		}
	case <-ctx.Done():
		t.Fatal("no accepted redispatch")
	}
}
