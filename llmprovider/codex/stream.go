package codex

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/llmprovider/openairesponses"
	openai "github.com/sashabaranov/go-openai"
)

var _ cogito.StreamingLLM = (*LLM)(nil)

// CreateChatCompletionStream emits only public summary text, answer text and
// function calls. Raw reasoning and encrypted_content never enter StreamEvent.
func (l *LLM) CreateChatCompletionStream(ctx context.Context, request openai.ChatCompletionRequest) (<-chan cogito.StreamEvent, error) {
	resp, err := l.openResponse(ctx, request)
	if err != nil {
		return nil, err
	}
	ch := make(chan cogito.StreamEvent, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		send := func(ev cogito.StreamEvent) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case ch <- ev:
				return nil
			}
		}
		state := streamState{send: send, text: make(map[textKey]string), tools: make(map[int]bool)}
		raw, err := readCompletedResponse(resp.Body, state.observe)
		if err == nil {
			var reply cogito.LLMReply
			var usage cogito.LLMUsage
			reply, usage, err = openairesponses.TranslateResponse(raw, request.Model)
			if err == nil {
				// The terminal output (or sorted output_item.done fallback) is authoritative.
				// Emit only suffixes not already delivered by live deltas.
				var final struct {
					Output []streamItem `json:"output"`
				}
				err = json.Unmarshal(raw, &final)
				if err == nil {
					for i, item := range final.Output {
						index := i
						if state.fallback && i < len(state.indices) {
							index = state.indices[i]
						}
						if err = state.item(index, item, true); err != nil {
							break
						}
					}
				}
				if err == nil {
					err = send(cogito.StreamEvent{Type: cogito.StreamEventDone, Usage: usage, FinishReason: string(reply.ChatCompletionResponse.Choices[0].FinishReason)})
				}
			}
		}
		if err != nil {
			// Do not block a cancelled consumer, even if it stopped draining the channel.
			if ctx.Err() != nil {
				select {
				case ch <- cogito.StreamEvent{Type: cogito.StreamEventError, Error: ctx.Err()}:
				default:
				}
				return
			}
			_ = send(cogito.StreamEvent{Type: cogito.StreamEventError, Error: err})
		}
	}()
	return ch, nil
}

type streamPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
type streamItem struct {
	Type      string       `json:"type"`
	Role      string       `json:"role"`
	CallID    string       `json:"call_id"`
	Name      string       `json:"name"`
	Arguments string       `json:"arguments"`
	Summary   []streamPart `json:"summary"`
	Content   []streamPart `json:"content"`
}
type textKey struct {
	kind       cogito.StreamEventType
	item, part int
}
type streamState struct {
	indices     []int
	fallback    bool
	send        func(cogito.StreamEvent) error
	text        map[textKey]string
	tools       map[int]bool
	lastSummary textKey
	hasSummary  bool
}

func (s *streamState) emit(k textKey, text string, full bool) error {
	if full {
		previous := s.text[k]
		if !strings.HasPrefix(text, previous) {
			return nil
		} // already streamed; cannot retract
		text = strings.TrimPrefix(text, previous)
	}
	if text == "" {
		return nil
	}
	if k.kind == cogito.StreamEventReasoning && s.text[k] == "" {
		if s.hasSummary && s.lastSummary != k {
			if err := s.send(cogito.StreamEvent{Type: k.kind, Content: "\n"}); err != nil {
				return err
			}
		}
		s.lastSummary = k
		s.hasSummary = true
	}
	s.text[k] += text
	ev := cogito.StreamEvent{Type: k.kind, Content: text}
	if k.kind == cogito.StreamEventToolCall {
		ev.Content = ""
		ev.ToolArgs = text
		ev.ToolCallIndex = k.item
	}
	return s.send(ev)
}
func (s *streamState) item(index int, item streamItem, full bool) error {
	switch item.Type {
	case "reasoning":
		for i, p := range item.Summary {
			if p.Type == "summary_text" && strings.TrimSpace(p.Text) != "" {
				if err := s.emit(textKey{cogito.StreamEventReasoning, index, i}, p.Text, full); err != nil {
					return err
				}
			}
		}
	case "message":
		if item.Role != "" && item.Role != "assistant" {
			return nil
		}
		for i, p := range item.Content {
			if p.Type == "output_text" {
				if err := s.emit(textKey{cogito.StreamEventContent, index, i}, p.Text, full); err != nil {
					return err
				}
			}
		}
	case "function_call":
		if !s.tools[index] {
			s.tools[index] = true
			if err := s.send(cogito.StreamEvent{Type: cogito.StreamEventToolCall, ToolCallIndex: index, ToolCallID: item.CallID, ToolName: item.Name}); err != nil {
				return err
			}
		}
		return s.emit(textKey{cogito.StreamEventToolCall, index, 0}, item.Arguments, full)
	}
	return nil
}
func (s *streamState) observe(raw []byte) error {
	var ev struct {
		Type         string     `json:"type"`
		OutputIndex  int        `json:"output_index"`
		SummaryIndex int        `json:"summary_index"`
		ContentIndex int        `json:"content_index"`
		Delta        string     `json:"delta"`
		Item         streamItem `json:"item"`
		Response     struct {
			Output []json.RawMessage `json:"output"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		return err
	}
	switch ev.Type {
	case "response.output_item.done":
		// Keep original wire indices when the terminal output array is empty.
		for _, index := range s.indices {
			if index == ev.OutputIndex {
				return nil
			}
		}
		s.indices = append(s.indices, ev.OutputIndex)
	case "response.completed":
		s.fallback = len(ev.Response.Output) == 0
		sort.Ints(s.indices)
	case "response.reasoning_summary_text.delta":
		return s.emit(textKey{cogito.StreamEventReasoning, ev.OutputIndex, ev.SummaryIndex}, ev.Delta, false)
	case "response.output_text.delta":
		return s.emit(textKey{cogito.StreamEventContent, ev.OutputIndex, ev.ContentIndex}, ev.Delta, false)
	case "response.function_call_arguments.delta":
		return s.emit(textKey{cogito.StreamEventToolCall, ev.OutputIndex, 0}, ev.Delta, false)
	case "response.output_item.added":
		if ev.Item.Type == "function_call" {
			return s.item(ev.OutputIndex, ev.Item, true)
		}
	}
	return nil
}
