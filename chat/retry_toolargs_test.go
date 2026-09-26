package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/mudler/cogito"
	openai "github.com/sashabaranov/go-openai"
)

// toolArgsStep answers one turn request of toolArgsLLM.
type toolArgsStep func(req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error)

// toolArgsLLM answers turn requests from script, in order, then with "ok".
// Summary requests are counted apart and always succeed. It records every
// turn request and every reasoning effort it is given.
type toolArgsLLM struct {
	mu        sync.Mutex
	script    []toolArgsStep
	repeat    toolArgsStep // answers every turn request when set
	reqs      []openai.ChatCompletionRequest
	summaries int
	efforts   []string
}

func (l *toolArgsLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	if err := ctx.Err(); err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}
	l.mu.Lock()
	if n := len(req.Messages); n > 0 && strings.HasPrefix(req.Messages[n-1].Content, compactInstruction) {
		l.summaries++
		l.mu.Unlock()
		return replyWith(fmt.Sprintf("summary %d", l.summaries)), cogito.LLMUsage{PromptTokens: 20, TotalTokens: 20}, nil
	}
	l.reqs = append(l.reqs, req)
	var step toolArgsStep
	switch {
	case l.repeat != nil:
		step = l.repeat
	case len(l.script) > 0:
		step = l.script[0]
		l.script = l.script[1:]
	}
	l.mu.Unlock()
	if step != nil {
		return step(req)
	}
	return replyWith("ok"), cogito.LLMUsage{PromptTokens: 10, CompletionTokens: 1, TotalTokens: 11}, nil
}

func (l *toolArgsLLM) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	l.mu.Lock()
	l.summaries++
	n := l.summaries
	l.mu.Unlock()
	return f.AddMessage("assistant", fmt.Sprintf("summary %d", n)), nil
}

func (l *toolArgsLLM) SetReasoningEffort(effort string) {
	l.mu.Lock()
	l.efforts = append(l.efforts, effort)
	l.mu.Unlock()
}

func (l *toolArgsLLM) requests() []openai.ChatCompletionRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]openai.ChatCompletionRequest(nil), l.reqs...)
}

// truncatedCall is a reply whose tool call was cut by finish_reason=length.
// Like cogito's clients, it reports the output cap the request carried.
func truncatedCall(tool, args, reasoning string, prompt, completion int) toolArgsStep {
	return func(req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
		return cogito.LLMReply{
				ChatCompletionResponse: openai.ChatCompletionResponse{Choices: []openai.ChatCompletionChoice{{
					Message: openai.ChatCompletionMessage{Role: "assistant", ToolCalls: []openai.ToolCall{{
						ID: "call_1", Type: openai.ToolTypeFunction,
						Function: openai.FunctionCall{Name: tool, Arguments: args},
					}}},
					FinishReason: openai.FinishReasonLength,
				}}},
				ReasoningContent: reasoning,
				MaxTokens:        max(req.MaxTokens, req.MaxCompletionTokens),
			}, cogito.LLMUsage{PromptTokens: prompt, CompletionTokens: completion, TotalTokens: prompt + completion},
			nil
	}
}

// toolArgsSession is newOverflowSession with a 100k window.
func toolArgsSession(t *testing.T, llm cogito.LLM, outputCap int) *Session {
	t.Helper()
	stubRetrySleep(t)
	s := newOverflowSession(t, llm)
	s.compaction.MaxContextTokens = 100000
	s.outputCap = outputCap
	return s
}

// lastUserNote returns the content of the request's last message when it is
// the one-turn truncation note, or "".
func lastUserNote(req openai.ChatCompletionRequest) string {
	if n := len(req.Messages); n > 0 {
		m := req.Messages[n-1]
		if m.Role == "user" && strings.Contains(m.Content, "Your previous") {
			return m.Content
		}
	}
	return ""
}

func historyHasNote(s *Session) bool {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	for _, m := range s.fragment.Messages {
		if strings.Contains(m.Content, "Your previous") {
			return true
		}
	}
	for _, m := range s.messages {
		if strings.Contains(m.Content, "Your previous") {
			return true
		}
	}
	return false
}

func TestRetryClassifiesCogitoSentinels(t *testing.T) {
	interrupted := fmt.Errorf("failed to make a streaming decision after 3 attempts: %w",
		fmt.Errorf("localai stream: body ended without [DONE] or a finish_reason: %w", cogito.ErrStreamInterrupted))
	invalid := fmt.Errorf("failed to make a decision after 3 attempts: %w",
		fmt.Errorf("%w: tool %q: %v", cogito.ErrToolArgumentsInvalid, "write", "unexpected end of JSON input"))
	truncated := fmt.Errorf("failed to make a decision after 1 attempts: %w", &cogito.ToolArgumentsTruncatedError{
		PromptTokens: 73000, CompletionTokens: 27000, MaxTokens: 99000, ToolName: "write", ArgumentsBytes: 108000,
	})
	cases := []struct {
		name string
		err  error
		want backendErrorClass
	}{
		{"stream interrupted", interrupted, errStreamInterrupted},
		{"invalid arguments", invalid, errToolArgs},
		{"truncated arguments are not retried as transient", truncated, errFatal},
		{"old cogito string form", errors.New("failed to make a streaming decision after 3 attempts: unexpected end of JSON input"), errTransient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyBackendError(tc.err); got != tc.want {
				t.Fatalf("classifyBackendError(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
	if k := classifyOverflow(truncated).Kind; k != KindNone {
		t.Fatalf("a truncated tool call classified as overflow kind %v", k)
	}
}

func TestRetryStreamInterruptedBudget(t *testing.T) {
	llm := &toolArgsLLM{repeat: func(openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
		return cogito.LLMReply{}, cogito.LLMUsage{},
			fmt.Errorf("localai stream: body ended without [DONE] or a finish_reason: %w", cogito.ErrStreamInterrupted)
	}}
	s := toolArgsSession(t, llm, 0)

	_, err := s.SendMessage("what changed?")
	if err == nil {
		t.Fatal("want an error")
	}
	// Each turn attempt makes requestAttempts calls (one cogito attempt).
	if got, want := len(llm.requests()), requestAttempts*(streamInterruptedBudget+1); got != want {
		t.Fatalf("requests = %d, want %d (%d turn retries)", got, want, streamInterruptedBudget)
	}
	if !strings.Contains(err.Error(), "the connection to the model ended before the reply finished") ||
		!strings.Contains(err.Error(), fmt.Sprintf("tried %d times", streamInterruptedBudget+1)) {
		t.Fatalf("err = %q, want the interrupted message", err)
	}
	if !errors.Is(err, cogito.ErrStreamInterrupted) {
		t.Fatal("the friendly error lost the sentinel")
	}
}

func TestToolArgsInvalidRetriedOnce(t *testing.T) {
	llm := &toolArgsLLM{repeat: func(openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
		return cogito.LLMReply{ChatCompletionResponse: openai.ChatCompletionResponse{Choices: []openai.ChatCompletionChoice{{
			Message: openai.ChatCompletionMessage{Role: "assistant", ToolCalls: []openai.ToolCall{{
				ID: "call_1", Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{Name: "write", Arguments: `{"path": "a.txt", "content": oops}`},
			}}},
			FinishReason: openai.FinishReasonToolCalls,
		}}}}, cogito.LLMUsage{PromptTokens: 100, CompletionTokens: 10}, nil
	}}
	s := toolArgsSession(t, llm, 0)

	_, err := s.SendMessage("write a file")
	if err == nil {
		t.Fatal("want an error")
	}
	if got, want := len(llm.requests()), toolArgsBudget+1; got != want {
		t.Fatalf("requests = %d, want %d", got, want)
	}
	if !strings.Contains(err.Error(), "the model produced a tool call with invalid arguments several times") {
		t.Fatalf("err = %q, want the invalid-arguments message", err)
	}
}

// The clamp lowered max_tokens and a tool call was truncated: the window ran
// out, and the prompt is large enough that compaction can make room.
func TestToolArgsTruncatedAfterClampCompacts(t *testing.T) {
	llm := &toolArgsLLM{script: []toolArgsStep{
		truncatedCall("write", `{"path":"a.txt","content":"`+strings.Repeat("x", 40000), "", 60000, 10000),
	}}
	s := toolArgsSession(t, llm, 100000)

	if _, err := s.SendMessage("write the file"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if llm.summaries == 0 {
		t.Fatal("the recovery chain did not run (no summary requested)")
	}
	if reqs := llm.requests(); len(reqs) != 2 {
		t.Fatalf("turn requests = %d, want 2 (one retry)", len(reqs))
	}
}

// The reported incident: a 73k prompt of a 100k window, below the compaction
// trigger, and a write call truncated at 27k completion tokens.
func TestToolArgsTruncatedIncidentNotesTheRetry(t *testing.T) {
	args := `{"path":"big.go","content":"` + strings.Repeat("y", 108000)
	llm := &toolArgsLLM{script: []toolArgsStep{truncatedCall("write", args, "", 73000, 27000)}}
	s := toolArgsSession(t, llm, 100000)

	if _, err := s.SendMessage("rewrite big.go"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if llm.summaries == 0 {
		t.Fatal("the recovery chain did not run")
	}
	reqs := llm.requests()
	if len(reqs) != 2 {
		t.Fatalf("turn requests = %d, want 2", len(reqs))
	}
	if lastUserNote(reqs[0]) != "" {
		t.Fatal("the first request carried the note")
	}
	note := lastUserNote(reqs[1])
	if !strings.Contains(note, "Your previous call to write was cut off after about") ||
		!strings.Contains(note, "Split it into smaller calls") {
		t.Fatalf("retry note = %q", note)
	}
	if historyHasNote(s) {
		t.Fatal("the one-turn note was persisted in the history")
	}
}

// Runaway reasoning: a small prompt, the output filled the window. No
// compaction, the output cap unchanged, the reasoning effort lowered for the
// turn only; a second truncation surfaces the reasoning message.
func TestToolArgsRunawayReasoning(t *testing.T) {
	reasoning := strings.Repeat("hmm ", 75000)
	step := truncatedCall("write", `{"path":"a`, reasoning, 20000, 79000)
	llm := &toolArgsLLM{script: []toolArgsStep{step, step}}
	s := toolArgsSession(t, llm, 100000)
	s.reasoningEffort = "high"

	_, err := s.SendMessage("do the big task")
	if err == nil {
		t.Fatal("want an error after the second truncation")
	}
	if llm.summaries != 0 || s.overflowRetries() != 0 {
		t.Fatalf("runaway reasoning compacted (summaries %d, retries %d)", llm.summaries, s.overflowRetries())
	}
	reqs := llm.requests()
	if len(reqs) != 2 {
		t.Fatalf("turn requests = %d, want 2", len(reqs))
	}
	note := lastUserNote(reqs[1])
	// The cap is unchanged: the retry reserves what the window leaves, less
	// only the note's own size.
	if cap, _ := s.requestLimits(); cap != 100000 {
		t.Fatalf("output cap after the turn = %d, want 100000", cap)
	}
	if d := reqs[0].MaxTokens - reqs[1].MaxTokens; d > len(note)/4+8 {
		t.Fatalf("the retry lowered max_tokens: %d then %d", reqs[0].MaxTokens, reqs[1].MaxTokens)
	}
	if !strings.Contains(note, "reasoning") || !strings.Contains(note, "smaller steps") {
		t.Fatalf("retry note = %q, want the reasoning note", note)
	}
	llm.mu.Lock()
	efforts := append([]string(nil), llm.efforts...)
	llm.mu.Unlock()
	if len(efforts) != 2 || efforts[0] != "medium" || efforts[1] != "high" {
		t.Fatalf("reasoning efforts set = %v, want [medium high] (lowered for the turn, then restored)", efforts)
	}
	if !strings.Contains(err.Error(), "the model's reasoning filled the context window") {
		t.Fatalf("err = %q, want the reasoning message", err)
	}
	if historyHasNote(s) {
		t.Fatal("the one-turn note was persisted in the history")
	}
}

// A long legitimate reply with a small prompt is never capped: no output
// ceiling exists, the request carries the whole cap.
func TestToolArgsNoOutputCeiling(t *testing.T) {
	llm := &toolArgsLLM{}
	s := toolArgsSession(t, llm, 60000)

	if _, err := s.SendMessage("write a long file"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if reqs := llm.requests(); len(reqs) != 1 || reqs[0].MaxTokens != 60000 {
		t.Fatalf("max_tokens = %v, want the full cap 60000", reqs[0].MaxTokens)
	}
}

// The cap was reached with room left in the window: not exhausted, so no
// compaction and the max_tokens message.
func TestToolArgsTruncatedNotExhausted(t *testing.T) {
	llm := &toolArgsLLM{repeat: func(req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
		return truncatedCall("write", `{"path":"a","content":"zz`, "", 20000, req.MaxTokens)(req)
	}}
	s := toolArgsSession(t, llm, 20000)

	_, err := s.SendMessage("write it")
	if err == nil {
		t.Fatal("want an error")
	}
	if llm.summaries != 0 {
		t.Fatalf("compacted %d times, want none", llm.summaries)
	}
	if !strings.Contains(err.Error(), "longer than the output limit (max_tokens") {
		t.Fatalf("err = %q, want the max_tokens message", err)
	}
	if lastUserNote(llm.requests()[len(llm.requests())-1]) != "" {
		t.Fatal("a note was sent although the turn was not retried")
	}
}

// No clamp (unknown limits) and no usage figures: no compaction, the
// max_tokens message.
func TestToolArgsTruncatedWithoutClamp(t *testing.T) {
	llm := &toolArgsLLM{repeat: truncatedCall("write", `{"path":"a","content":"zz`, "", 0, 0)}
	s := toolArgsSession(t, llm, 0)

	_, err := s.SendMessage("write it")
	if err == nil {
		t.Fatal("want an error")
	}
	if llm.summaries != 0 {
		t.Fatalf("compacted %d times, want none", llm.summaries)
	}
	if len(llm.requests()) != 1 {
		t.Fatalf("turn requests = %d, want 1 (no retry)", len(llm.requests()))
	}
	if !strings.Contains(err.Error(), "max_tokens") {
		t.Fatalf("err = %q, want the max_tokens message", err)
	}
}
