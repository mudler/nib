package agentmcp

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
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
)

// bgShellLLM scripts a turn that backgrounds a shell job: request 1 calls
// bash_background with a short sleep, request 2 is the park reply, request 3
// (after the job's completion is injected) is the final reply. It answers both
// streaming and non-streaming calls.
func bgShellLLM(t *testing.T, script, parkReply, finalReply string) *httptest.Server {
	t.Helper()
	var req int64
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream bool `json:"stream"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)

		var (
			isTool bool
			name   string
			args   string
			text   string
		)
		switch atomic.AddInt64(&req, 1) {
		case 1:
			isTool, name, args = true, "bash_background", `{"script":"`+script+`"}`
		case 2:
			text = parkReply
		default:
			text = finalReply
		}

		if !body.Stream {
			msg := map[string]any{"role": "assistant"}
			finish := "stop"
			if isTool {
				msg["content"] = nil
				msg["tool_calls"] = []any{map[string]any{
					"id": "call_bg", "type": "function", "index": 0,
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
				"index": 0, "id": "call_bg", "type": "function",
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

// TestVoiceE2EShellJobParkThenNotify proves the shellJobs wiring: a backgrounded
// shell job (bash_background) keeps the converse run parked (pending=true), and
// its completion injects a notice that resumes the run, whose final reply is
// pushed as a nib/reply notification. Without Run's SetShellJobs wiring the run
// would not park on the job and no notification would arrive.
func TestVoiceE2EShellJobParkThenNotify(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))
	const parkReply = "Started it in the background."
	const finalReply = "the background job finished"

	llm := bgShellLLM(t, "sleep 2", parkReply, finalReply)
	defer llm.Close()

	cfg := types.Config{
		Model: "test", APIKey: "sk", BaseURL: llm.URL, Prompt: "You are nib.",
		ApprovalMode: "auto",
		AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 3},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Real tool transports + the shared shell-job registry (as main.go wires it).
	shellJobs := wizmcp.NewShellJobs()
	transports, err := wizmcp.StartTransports(ctx, cfg, shellJobs)
	if err != nil {
		t.Fatalf("StartTransports: %v", err)
	}

	r := newRouter()
	sess, err := chat.NewSession(ctx, cfg, buildCallbacks(r, newPolicy(cfg)), transports...)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()
	sess.SetShellJobs(shellJobs) // the wiring under test (Run does this for real)

	srvT, cliT := mcp.NewInMemoryTransports()
	srv := newServer(ctx, sess, r)
	go func() { _ = srv.Run(ctx, srvT) }()

	says := make(chan replyPayload, 8)
	client := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "v0"}, &mcp.ClientOptions{
		LoggingMessageHandler: func(_ context.Context, req *mcp.LoggingMessageRequest) {
			if b, ok := req.Params.Data.(map[string]any); ok {
				says <- replyPayload{Kind: asString(b["kind"]), Text: asString(b["text"])}
			}
		},
	})
	cs, err := client.Connect(ctx, cliT, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()
	if err := cs.SetLoggingLevel(ctx, &mcp.SetLoggingLevelParams{Level: "info"}); err != nil {
		t.Fatalf("set logging level: %v", err)
	}

	callCtx, callCancel := context.WithTimeout(ctx, 20*time.Second)
	defer callCancel()
	res, err := cs.CallTool(callCtx, &mcp.CallToolParams{
		Name: "converse", Arguments: map[string]any{"utterance": "run something in the background"},
	})
	if err != nil {
		t.Fatalf("converse: %v", err)
	}
	var out converseOut
	decodeStructured(t, res, &out)
	if !out.Pending {
		t.Fatalf("converse out = %+v, want pending=true (the run should park on the background shell job)", out)
	}
	if out.Reply != parkReply {
		t.Fatalf("converse reply = %q, want park reply %q", out.Reply, parkReply)
	}

	for {
		select {
		case s := <-says:
			if s.Kind == "reply" && s.Text == finalReply {
				return // success: the shell job's completion pushed a nib/reply
			}
		case <-time.After(20 * time.Second):
			t.Fatalf("no nib/reply carrying the final reply %q after the shell job finished", finalReply)
		}
	}
}
