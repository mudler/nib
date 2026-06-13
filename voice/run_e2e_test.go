package voice

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
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
