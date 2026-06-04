package trace

import (
	"context"

	"github.com/mudler/cogito"
	openai "github.com/sashabaranov/go-openai"
)

// recordingStreamingLLM extends recordingLLM with streaming. It forwards every
// StreamEvent unchanged and records a best-effort reassembled response when the
// stream completes. nib sets no stream callback today, so cogito does not
// currently exercise this path; it exists to keep the StreamingLLM type
// assertion transparent (so adding streaming later is captured automatically).
type recordingStreamingLLM struct {
	*recordingLLM
	stream cogito.StreamingLLM
}

// CreateChatCompletionStream taps the inner stream: it forwards every event
// unchanged and records one reassembled "stream" Record when the stream ends
// (or the error, if the stream failed mid-way). The consumer must drain the
// returned channel or cancel ctx; otherwise the forwarding goroutine blocks.
func (l *recordingStreamingLLM) CreateChatCompletionStream(ctx context.Context, request openai.ChatCompletionRequest) (<-chan cogito.StreamEvent, error) {
	in, err := l.stream.CreateChatCompletionStream(ctx, request)
	if err != nil {
		req := request
		if req.Model == "" {
			req.Model = l.model
		}
		l.write(Record{Model: l.model, AgentID: l.agentID, Method: "stream", Request: &req, Error: err.Error()})
		return nil, err
	}

	out := make(chan cogito.StreamEvent)
	go func() {
		defer close(out)

		var content string
		toolCalls := map[int]*openai.ToolCall{}
		var order []int
		var finish string
		var usage cogito.LLMUsage
		var streamErr error

		for ev := range in {
			switch ev.Type {
			case cogito.StreamEventContent:
				content += ev.Content
			case cogito.StreamEventToolCall:
				tc, ok := toolCalls[ev.ToolCallIndex]
				if !ok {
					tc = &openai.ToolCall{Type: openai.ToolTypeFunction}
					toolCalls[ev.ToolCallIndex] = tc
					order = append(order, ev.ToolCallIndex)
				}
				if ev.ToolCallID != "" {
					tc.ID = ev.ToolCallID
				}
				if ev.ToolName != "" {
					tc.Function.Name = ev.ToolName
				}
				tc.Function.Arguments += ev.ToolArgs
			case cogito.StreamEventDone:
				finish = ev.FinishReason
				usage = ev.Usage
			case cogito.StreamEventError:
				streamErr = ev.Error
			}

			// Forward unchanged; stop if the consumer's context is cancelled.
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}

		req := request
		if req.Model == "" {
			req.Model = l.model
		}
		rec := Record{Model: l.model, AgentID: l.agentID, Method: "stream", Request: &req}
		if streamErr != nil {
			rec.Error = streamErr.Error()
		} else {
			msg := openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: content}
			for _, i := range order {
				msg.ToolCalls = append(msg.ToolCalls, *toolCalls[i])
			}
			rec.Response = &openai.ChatCompletionResponse{
				Model:   l.model,
				Choices: []openai.ChatCompletionChoice{{Index: 0, Message: msg, FinishReason: openai.FinishReason(finish)}},
				Usage: openai.Usage{
					PromptTokens:     usage.PromptTokens,
					CompletionTokens: usage.CompletionTokens,
					TotalTokens:      usage.TotalTokens,
				},
			}
		}
		l.write(rec)
	}()
	return out, nil
}
