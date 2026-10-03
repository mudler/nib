package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mudler/cogito"
	openai "github.com/sashabaranov/go-openai"
)

type noticeCaptureLLM struct {
	requests []openai.ChatCompletionRequest
	err      error
	onCall   func(openai.ChatCompletionRequest)
}

func (l *noticeCaptureLLM) Ask(_ context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	return f, l.err
}

func (l *noticeCaptureLLM) CreateChatCompletion(_ context.Context, request openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	l.requests = append(l.requests, request)
	if l.onCall != nil {
		l.onCall(request)
	}
	return cogito.LLMReply{}, cogito.LLMUsage{}, l.err
}

func publishNotice(t *testing.T, state *backgroundState, source backgroundSource, id, content string) {
	t.Helper()
	if !state.startBackground(source, id) {
		t.Fatalf("start %q", id)
	}
	if !state.completeBackground(source, id, content, true) {
		t.Fatalf("complete %q", id)
	}
}

func requestNoticeMessage(t *testing.T, request openai.ChatCompletionRequest) string {
	t.Helper()
	for _, message := range request.Messages {
		if strings.HasPrefix(message.Content, "Background work completed") {
			if message.Role != openai.ChatMessageRoleUser {
				t.Fatalf("notice role = %q", message.Role)
			}
			return message.Content
		}
	}
	return ""
}

type noticeStreamingLLM struct {
	noticeCaptureLLM
	events chan cogito.StreamEvent
}

func (l *noticeStreamingLLM) CreateChatCompletionStream(_ context.Context, request openai.ChatCompletionRequest) (<-chan cogito.StreamEvent, error) {
	l.requests = append(l.requests, request)
	return l.events, nil
}

func TestNoticeDeliveryStreamingErrorRollsBackWithoutWaitingForClose(t *testing.T) {
	state := newBackgroundState()
	publishNotice(t, state, backgroundAgent, "worker", "failed stream")
	upstream := make(chan cogito.StreamEvent, 1)
	llm := &noticeStreamingLLM{events: upstream}
	wrapped := withNoticeDelivery(llm, state).(cogito.StreamingLLM)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := wrapped.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	upstream <- cogito.StreamEvent{Type: cogito.StreamEventError, Error: errors.New("boom")}
	select {
	case event := <-events:
		if event.Type != cogito.StreamEventError {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("relay waited for upstream close after error")
	}
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("relay remained open")
		}
	case <-time.After(time.Second):
		t.Fatal("relay did not close")
	}
	_, notices := state.reserveNotices(0)
	if len(notices) != 1 {
		t.Fatalf("rolled back notices = %d", len(notices))
	}
}

func TestNoticeDeliveryIncludesEveryNoticeBeyondLegacyCap(t *testing.T) {
	state := newBackgroundState()
	for i := 0; i < maxPendingNotices+9; i++ {
		publishNotice(t, state, backgroundAgent, fmt.Sprintf("agent-%02d", i), fmt.Sprintf("result-%02d", i))
	}
	capture := &noticeCaptureLLM{}
	llm := withNoticeDelivery(capture, state)

	_, _, err := llm.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	message := requestNoticeMessage(t, capture.requests[0])
	for i := 0; i < maxPendingNotices+9; i++ {
		needle := fmt.Sprintf("source=agent id=agent-%02d\nresult-%02d", i, i)
		if strings.Count(message, needle) != 1 {
			t.Fatalf("%q count = %d\n%s", needle, strings.Count(message, needle), message)
		}
	}
	if snap := state.terminalSnapshot(); snap.queuedNotices != 0 || snap.reservedNotices != 0 {
		t.Fatalf("notices after handoff: %+v", snap)
	}
}

func TestNoticeDeliveryMixedSourcesSequenceOrderExactlyOnce(t *testing.T) {
	state := newBackgroundState()
	publishNotice(t, state, backgroundAgent, "agent-a", "agent result")
	publishNotice(t, state, backgroundShell, "shell-b", "shell result")
	publishNotice(t, state, backgroundAgent, "agent-c", "second result")
	capture := &noticeCaptureLLM{}
	llm := withNoticeDelivery(capture, state)

	for range 2 {
		if _, _, err := llm.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	first := requestNoticeMessage(t, capture.requests[0])
	want := []string{"source=agent id=agent-a", "source=shell id=shell-b", "source=agent id=agent-c"}
	position := -1
	for _, needle := range want {
		if strings.Count(first, needle) != 1 {
			t.Fatalf("%q not exactly once in %q", needle, first)
		}
		next := strings.Index(first, needle)
		if next <= position {
			t.Fatalf("notices out of sequence: %q", first)
		}
		position = next
	}
	if second := requestNoticeMessage(t, capture.requests[1]); second != "" {
		t.Fatalf("notice redelivered: %q", second)
	}
}

func TestNoticeDeliveryRollsBackRequestStartupFailure(t *testing.T) {
	state := newBackgroundState()
	publishNotice(t, state, backgroundShell, "shell-7", "build done")
	capture := &noticeCaptureLLM{err: errors.New("startup failed")}
	llm := withNoticeDelivery(capture, state)

	if _, _, err := llm.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{}); err == nil {
		t.Fatal("expected startup error")
	}
	if snap := state.terminalSnapshot(); snap.queuedNotices != 1 || snap.reservedNotices != 0 {
		t.Fatalf("reservation was not rolled back: %+v", snap)
	}
	capture.err = nil
	if _, _, err := llm.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(requestNoticeMessage(t, capture.requests[1]), "source=shell id=shell-7"); got != 1 {
		t.Fatalf("retry delivery count = %d", got)
	}
}

func TestNoticeDeliveryConsumesOnlyAfterHandoff(t *testing.T) {
	state := newBackgroundState()
	publishNotice(t, state, backgroundAgent, "agent-1", "done")
	capture := &noticeCaptureLLM{}
	capture.onCall = func(request openai.ChatCompletionRequest) {
		if requestNoticeMessage(t, request) == "" {
			t.Fatal("handoff omitted notice")
		}
		snap := state.terminalSnapshot()
		if snap.reservedNotices != 1 || snap.queuedNotices != 0 {
			t.Fatalf("notice consumed before handoff: %+v", snap)
		}
		if snap.rootObservedSequence == 0 {
			t.Fatalf("request included notice without observation: %+v", snap)
		}
	}
	if _, _, err := withNoticeDelivery(capture, state).CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{}); err != nil {
		t.Fatal(err)
	}
	if snap := state.terminalSnapshot(); snap.reservedNotices != 0 {
		t.Fatalf("notice not consumed after handoff: %+v", snap)
	}
}

func TestNoticeDeliveryFiltersLegacyCompletionMarker(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: "user", Content: "ordinary"},
		{Role: "user", Name: agentCompletionMessageName, Content: "duplicate completion"},
	}
	got := filterLegacyAgentCompletions(messages)
	if len(got) != 1 || got[0].Content != "ordinary" {
		t.Fatalf("filtered messages = %#v", got)
	}
}
