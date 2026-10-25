package chat

import (
	"context"
	"strings"

	"github.com/mudler/cogito"
	openai "github.com/sashabaranov/go-openai"
)

// inputSourceLLM asks the host for texts before each model request and hands
// them to the model as one user message, in the order the host returned them.
// It sits at the same last-possible handoff as noticeDeliveryLLM, so a retry or
// a multi-step tool turn sees each text once, on the request that carries it.
type inputSourceLLM struct {
	cogito.LLM
	source func() ([]string, func(error))
}

type inputSourceStreamingLLM struct {
	*inputSourceLLM
	stream cogito.StreamingLLM
}

// withInputSource returns llm unchanged when source is nil.
func withInputSource(llm cogito.LLM, source func() ([]string, func(error))) cogito.LLM {
	if source == nil {
		return llm
	}
	base := &inputSourceLLM{LLM: llm, source: source}
	if stream, ok := llm.(cogito.StreamingLLM); ok {
		return &inputSourceStreamingLLM{inputSourceLLM: base, stream: stream}
	}
	return base
}

// include appends the host texts as one user message. The returned messages
// own a fresh backing array, so the caller's fragment is never overwritten.
func (l *inputSourceLLM) include(messages []openai.ChatCompletionMessage) ([]openai.ChatCompletionMessage, func(error)) {
	texts, settle := l.source()
	kept := make([]string, 0, len(texts))
	for _, t := range texts {
		if strings.TrimSpace(t) != "" {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		if settle != nil {
			settle(nil)
		}
		return messages, nil
	}
	out := append(make([]openai.ChatCompletionMessage, 0, len(messages)+1), messages...)
	out = append(out, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: strings.Join(kept, "\n\n"),
	})
	return out, settle
}

func (l *inputSourceLLM) Ask(ctx context.Context, fragment cogito.Fragment) (cogito.Fragment, error) {
	var settle func(error)
	fragment.Messages, settle = l.include(fragment.Messages)
	out, err := l.LLM.Ask(ctx, fragment)
	if settle != nil {
		settle(err)
	}
	return out, err
}

func (l *inputSourceLLM) CreateChatCompletion(ctx context.Context, request openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	var settle func(error)
	request.Messages, settle = l.include(request.Messages)
	reply, usage, err := l.LLM.CreateChatCompletion(ctx, request)
	if settle != nil {
		settle(err)
	}
	return reply, usage, err
}

func (l *inputSourceStreamingLLM) CreateChatCompletionStream(ctx context.Context, request openai.ChatCompletionRequest) (<-chan cogito.StreamEvent, error) {
	var settle func(error)
	request.Messages, settle = l.include(request.Messages)
	events, err := l.stream.CreateChatCompletionStream(ctx, request)
	if err != nil {
		if settle != nil {
			settle(err)
		}
		return nil, err
	}
	if settle == nil {
		return events, nil
	}
	out := make(chan cogito.StreamEvent)
	go func() {
		defer close(out)
		settled := false
		finish := func(e error) {
			if !settled {
				settled = true
				settle(e)
			}
		}
		defer finish(context.Canceled) // the stream ended without a done event
		for {
			select {
			case <-ctx.Done():
				finish(ctx.Err())
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				switch event.Type {
				case cogito.StreamEventDone:
					finish(nil)
				case cogito.StreamEventError:
					e := event.Error
					if e == nil {
						e = context.Canceled
					}
					finish(e)
				}
				select {
				case out <- event:
				case <-ctx.Done():
					finish(ctx.Err())
					return
				}
			}
		}
	}()
	return out, nil
}
