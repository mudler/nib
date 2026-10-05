package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/chat"
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/types"
)

// jsonWriter emits one JSON object per line. The agent calls back from its own
// goroutine, so writes are serialized to keep lines whole.
type jsonWriter struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func newJSONWriter(w io.Writer) *jsonWriter {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &jsonWriter{enc: enc}
}

func (j *jsonWriter) emit(typ string, fields map[string]any) {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["type"] = typ
	j.mu.Lock()
	defer j.mu.Unlock()
	_ = j.enc.Encode(fields)
}

// RunJSON is the machine-readable counterpart of RunCLI, for scripts and for
// debugging what a session emits. Each stdin line is one user message ("exit"
// ends the session); stdout carries only JSON Lines events, in the order the
// agent produced them:
//
//	reasoning_delta / content_delta  live streamed text ("agent" set for sub-agents)
//	reasoning                        a step's complete reasoning (step boundary)
//	step_content                     commentary that accompanied a tool selection
//	tool_call                        a call needing approval; approved is false, as
//	                                 nothing can answer it (use --yolo to allow)
//	tool_start / tool_result         a call running / its output
//	agent                            sub-agent lifecycle change
//	response                         the turn's final reply
//	error                            a failure
//	usage                            token totals, once, when the session ends
//
// Logs stay on stderr. Exit behavior matches RunCLI: a denied call ends the
// session with ErrApprovalNoInput.
func RunJSON(ctx context.Context, cfg types.Config, streams Streams, shellJobs *wizmcp.ShellJobs, artifacts *wizmcp.ArtifactStore, transports ...mcp.Transport) error {
	in, out := streams.stdin(), streams.stdout()
	reader := bufio.NewReader(in)
	jw := newJSONWriter(out)
	var denied atomic.Bool

	callbacks := chat.Callbacks{
		OnGoalPaused: func(n chat.GoalPausedNotice) {
			jw.emit("goal_paused", map[string]any{"kind": "goal_paused", "max_reprompts": n.MaxReprompts, "window": n.Window.String(), "paused": n.Paused, "message": "The goal remains paused until explicit resume or replacement through a supported host."})
		},
		OnStream: func(ev chat.StreamEvent) {
			var typ string
			switch ev.Kind {
			case "reasoning":
				typ = "reasoning_delta"
			case "content":
				typ = "content_delta"
			default:
				return
			}
			f := map[string]any{"text": ev.Content}
			if ev.AgentID != "" {
				f["agent"] = ev.AgentID
			}
			jw.emit(typ, f)
		},
		OnReasoning: func(reasoning string) {
			jw.emit("reasoning", map[string]any{"text": reasoning})
		},
		OnStepContent: func(content string) {
			jw.emit("step_content", map[string]any{"text": content})
		},
		OnToolCall: func(req chat.ToolCallRequest) chat.ToolCallResponse {
			denied.Store(true)
			f := map[string]any{"name": req.Name, "arguments": req.Arguments, "approved": false}
			if req.AgentID != "" {
				f["agent"] = req.AgentID
			}
			jw.emit("tool_call", f)
			return chat.ToolCallResponse{Approved: false}
		},
		OnToolStart: func(ts chat.ToolStart) {
			jw.emit("tool_start", map[string]any{"name": ts.Name, "arguments": ts.Arguments})
		},
		OnToolResult: func(res chat.ToolResult) {
			f := map[string]any{"name": res.Name, "arguments": res.Arguments, "result": res.Result}
			if res.AgentID != "" {
				f["agent"] = res.AgentID
			}
			jw.emit("tool_result", f)
		},
		OnAgentEvent: func(ev chat.AgentEvent) {
			f := map[string]any{"id": ev.ID, "agent_type": ev.Type, "task": ev.Task, "status": ev.Status, "background": ev.Background}
			if ev.Err != nil {
				f["error"] = ev.Err.Error()
			}
			jw.emit("agent", f)
		},
		OnResponse: func(response string) {
			jw.emit("response", map[string]any{"text": response})
		},
		OnError: func(err error) {
			jw.emit("error", map[string]any{"message": err.Error()})
		},
	}

	session, err := chat.NewSession(ctx, cfg, callbacks, transports...)
	if err != nil {
		return err
	}
	defer session.Close()
	defer func() {
		u := session.Usage()
		jw.emit("usage", map[string]any{
			"prompt_tokens": u.PromptTokens, "completion_tokens": u.CompletionTokens,
			"total_tokens": u.TotalTokens, "turns": u.Turns,
		})
	}()
	if shellJobs != nil {
		session.SetShellJobs(shellJobs)
	}
	session.UseArtifactStore(artifacts)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		text, rerr := readStringCancellable(ctx, reader)
		eof := errors.Is(rerr, io.EOF)
		if rerr != nil && !eof {
			return rerr
		}
		if text = strings.TrimSpace(text); text != "" {
			if text == "exit" {
				break
			}
			if _, err := session.SendMessage(text); err != nil {
				jw.emit("error", map[string]any{"message": err.Error()})
			}
		}
		if eof {
			break
		}
	}
	if denied.Load() {
		return ErrApprovalNoInput
	}
	return nil
}
