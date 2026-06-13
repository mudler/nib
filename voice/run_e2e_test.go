package voice

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
)

// fakeLLM returns one assistant message with no tool calls: the model just
// answers. This drives a single-turn converse with no background work. It
// answers both streaming and non-streaming requests so it works regardless of
// how the cogito OpenAI client phrases the call.
func fakeLLM(t *testing.T, answer string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream bool `json:"stream"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)

		if !req.Stream {
			resp := map[string]any{
				"id": "1", "object": "chat.completion", "model": "fake",
				"choices": []map[string]any{{
					"index":         0,
					"message":       map[string]any{"role": "assistant", "content": answer},
					"finish_reason": "stop",
				}},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		emit := func(delta map[string]any, finish string) {
			choice := map[string]any{"index": 0, "delta": delta}
			if finish != "" {
				choice["finish_reason"] = finish
			}
			b, _ := json.Marshal(map[string]any{
				"id": "fake", "object": "chat.completion.chunk", "model": "fake",
				"choices": []any{choice},
			})
			_, _ = w.Write([]byte("data: "))
			_, _ = w.Write(b)
			_, _ = w.Write([]byte("\n\n"))
			if fl != nil {
				fl.Flush()
			}
		}
		emit(map[string]any{"content": answer}, "")
		emit(map[string]any{}, "stop")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if fl != nil {
			fl.Flush()
		}
	}))
}

// parkingLLM scripts a turn that backgrounds a sub-agent and parks on it,
// mirroring chat/session_park_reply_test.go. The main conversation: req#1 spawns
// a background sub-agent, req#2 replies with parkReply (the run parks while the
// sub-agent runs), and once the sub-agent's completion is injected the run
// resumes and answers finalReply. The sub-agent (its conversation opens with
// user "subtask") blocks until release is closed. It answers both streaming and
// non-streaming requests.
func parkingLLM(t *testing.T, release <-chan struct{}, parkReply, finalReply string) *httptest.Server {
	t.Helper()
	var mainReq int64
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream   bool `json:"stream"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)

		firstUser := ""
		for _, m := range req.Messages {
			if m.Role == "user" {
				firstUser = m.Content
				break
			}
		}

		var (
			isTool bool
			name   string
			args   string
			text   string
		)
		if firstUser == "subtask" {
			select {
			case <-release:
			case <-time.After(10 * time.Second):
			}
			text = "sub reply"
		} else {
			switch atomic.AddInt64(&mainReq, 1) {
			case 1:
				isTool, name, args = true, "spawn_agent", `{"task":"subtask","background":true}`
			case 2:
				text = parkReply
			default:
				text = finalReply
			}
		}

		if !req.Stream {
			msg := map[string]any{"role": "assistant"}
			finish := "stop"
			if isTool {
				msg["content"] = nil
				msg["tool_calls"] = []any{map[string]any{
					"id": "call_spawn", "type": "function", "index": 0,
					"function": map[string]any{"name": name, "arguments": args},
				}}
				finish = "tool_calls"
			} else {
				msg["content"] = text
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "fake", "object": "chat.completion", "model": "fake",
				"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
			})
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		emit := func(delta map[string]any, finish string) {
			choice := map[string]any{"index": 0, "delta": delta}
			if finish != "" {
				choice["finish_reason"] = finish
			}
			b, _ := json.Marshal(map[string]any{
				"id": "fake", "object": "chat.completion.chunk", "model": "fake",
				"choices": []any{choice},
			})
			_, _ = w.Write([]byte("data: "))
			_, _ = w.Write(b)
			_, _ = w.Write([]byte("\n\n"))
			if fl != nil {
				fl.Flush()
			}
		}
		if isTool {
			emit(map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": "call_spawn", "type": "function",
				"function": map[string]any{"name": name, "arguments": args},
			}}}, "")
			emit(map[string]any{}, "tool_calls")
		} else {
			emit(map[string]any{"content": text}, "")
			emit(map[string]any{}, "stop")
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if fl != nil {
			fl.Flush()
		}
	}))
}

// TestVoiceE2EParkThenNotify drives a REAL chat.Session (fake LLM) through a
// turn that parks on a backgrounded sub-agent: converse must return at the park
// with pending=true, and once the sub-agent finishes the resumed run's final
// reply must arrive as a nib/say notification (which requires the client to have
// set its logging level to info).
func TestVoiceE2EParkThenNotify(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))
	const parkReply = "Working on it in the background."
	const finalReply = "all done"
	release := make(chan struct{})
	llm := parkingLLM(t, release, parkReply, finalReply)
	defer llm.Close()

	cfg := types.Config{
		Model: "test", APIKey: "sk", BaseURL: llm.URL, Prompt: "You are nib.",
		ApprovalMode: "auto",
		AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 3},
	}
	cfg = applyProfile(cfg)

	r := newRouter()
	sess, err := chat.NewSession(context.Background(), cfg, buildCallbacks(r, newPolicy(cfg)))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	srvT, cliT := mcp.NewInMemoryTransports()
	srv := newServer(context.Background(), sess, r)
	go func() { _ = srv.Run(context.Background(), srvT) }()

	says := make(chan sayPayload, 8)
	client := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "v0"}, &mcp.ClientOptions{
		LoggingMessageHandler: func(_ context.Context, req *mcp.LoggingMessageRequest) {
			if b, ok := req.Params.Data.(map[string]any); ok {
				says <- sayPayload{Kind: asString(b["kind"]), Text: asString(b["text"]), Message: asString(b["message"])}
			}
		},
	})
	cs, err := client.Connect(context.Background(), cliT, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()
	if err := cs.SetLoggingLevel(context.Background(), &mcp.SetLoggingLevelParams{Level: "info"}); err != nil {
		t.Fatalf("set logging level: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "converse", Arguments: map[string]any{"utterance": "start"},
	})
	if err != nil {
		t.Fatalf("converse: %v", err)
	}
	var out converseOut
	decodeStructured(t, res, &out)
	if !out.Pending {
		t.Fatalf("converse out = %+v, want pending=true at the park", out)
	}
	if out.Reply != parkReply {
		t.Fatalf("converse reply = %q, want park reply %q", out.Reply, parkReply)
	}

	// Let the sub-agent finish: its completion resumes the parked run, whose
	// final reply must arrive as a nib/say notification.
	close(release)
	for {
		select {
		case s := <-says:
			if s.Kind == "say" && s.Text == finalReply {
				return // success
			}
			// Ignore any other say (e.g. an interim reply); keep waiting.
		case <-time.After(15 * time.Second):
			t.Fatalf("no nib/say carrying the final reply %q", finalReply)
		}
	}
}

func TestVoiceE2ESingleTurn(t *testing.T) {
	llm := fakeLLM(t, "Two plus two is four.")
	defer llm.Close()

	cfg := types.Config{
		Model: "test", APIKey: "sk", BaseURL: llm.URL, Prompt: "You are nib.",
		ApprovalMode: "auto",
		AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 3},
	}
	cfg = applyProfile(cfg)

	r := newRouter()
	sess, err := chat.NewSession(context.Background(), cfg, buildCallbacks(r, newPolicy(cfg)))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	srvT, cliT := mcp.NewInMemoryTransports()
	srv := newServer(context.Background(), sess, r)
	go func() { _ = srv.Run(context.Background(), srvT) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
	cs, err := client.Connect(context.Background(), cliT, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "converse", Arguments: map[string]any{"utterance": "what is two plus two?"},
	})
	if err != nil {
		t.Fatalf("converse: %v", err)
	}
	var out converseOut
	decodeStructured(t, res, &out)
	if out.Reply == "" {
		t.Fatalf("expected a spoken reply, got empty (out=%+v)", out)
	}
}
