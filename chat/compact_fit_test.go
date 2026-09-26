package chat

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mudler/cogito"
	openai "github.com/sashabaranov/go-openai"
)

// promptCapturingLLM records every summary prompt it is asked, and rejects any
// prompt whose byte/4 estimate exceeds limit the way llama.cpp rejects a
// request larger than its context. limit 0 accepts everything.
type promptCapturingLLM struct {
	limit   int
	prompts []string
}

func (f *promptCapturingLLM) Ask(ctx context.Context, fr cogito.Fragment) (cogito.Fragment, error) {
	return cogito.Fragment{}, errors.New("the summary must be sent with CreateChatCompletion, not Ask")
}

func (f *promptCapturingLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	prompt := req.Messages[len(req.Messages)-1].Content
	f.prompts = append(f.prompts, prompt)
	if f.limit > 0 && tokensOf(prompt) > f.limit {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("rpc error: code = Internal desc = request (%d tokens) exceeds the available context size (%d tokens), try increasing it", tokensOf(prompt), f.limit)
	}
	return cogito.LLMReply{ChatCompletionResponse: openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Role: "assistant", Content: "SUMMARY"}}},
	}}, cogito.LLMUsage{}, nil
}

// toolTurn is one assistant tool call plus its result.
func toolTurn(id, path, body string) []openai.ChatCompletionMessage {
	return []openai.ChatCompletionMessage{
		{Role: "assistant", ToolCalls: []openai.ToolCall{{ID: id, Function: openai.FunctionCall{Name: "read", Arguments: `{"path":"` + path + `"}`}}}},
		{Role: "tool", ToolCallID: id, Content: body},
	}
}

func TestCompactHistorySummarizesThePrunedView(t *testing.T) {
	// The request the model sees has this result stubbed, so the summary
	// prompt must not resurrect the full body: that is how a session whose
	// requests fit the window produced a summary prompt nearly twice as big.
	big := strings.Repeat("BODY", 5000)
	frag := []openai.ChatCompletionMessage{{Role: "user", Content: "goal: fix the parser"}}
	frag = append(frag, toolTurn("call-1", "parser.go", big)...)
	frag = append(frag,
		openai.ChatCompletionMessage{Role: "assistant", Content: "read it"},
		openai.ChatCompletionMessage{Role: "user", Content: "u2"},
		openai.ChatCompletionMessage{Role: "assistant", Content: "a2"},
	)
	llm := &promptCapturingLLM{}
	s := newCompactTestSession(llm, 2, frag, frag)
	s.compaction.MaxContextTokens = 1 << 20
	s.prunedIDs = map[string]string{"call-1": detailBudget}

	if _, _, err := s.CompactHistory(); err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}
	if len(llm.prompts) != 1 {
		t.Fatalf("summary calls = %d, want 1", len(llm.prompts))
	}
	if strings.Contains(llm.prompts[0], "BODYBODY") {
		t.Fatal("summary prompt carries the full body of a result the requests already stubbed")
	}
	if !strings.Contains(llm.prompts[0], prunedStub("read", "parser.go", detailBudget)) {
		t.Fatalf("summary prompt should carry the stub instead, got %q", llm.prompts[0])
	}
}

func TestCompactHistoryFitsTheSummaryPromptInTheBudget(t *testing.T) {
	// Nothing is stubbed, and the head alone is far larger than the window.
	frag := []openai.ChatCompletionMessage{{Role: "user", Content: "goal: fix the parser"}}
	for i := range 6 {
		frag = append(frag, toolTurn(fmt.Sprintf("c%d", i), fmt.Sprintf("f%d.go", i), strings.Repeat("x", 8000))...)
	}
	frag = append(frag,
		openai.ChatCompletionMessage{Role: "assistant", Content: "done reading"},
		openai.ChatCompletionMessage{Role: "user", Content: "u2"},
		openai.ChatCompletionMessage{Role: "assistant", Content: "a2"},
	)
	llm := &promptCapturingLLM{}
	s := newCompactTestSession(llm, 2, frag, frag)
	s.compaction.MaxContextTokens = 4000
	budget := ContextBudget(s.compaction, 4000)

	if _, _, err := s.CompactHistory(); err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}
	// The head is known not to fit, so it goes out in chunks from the start.
	for i, p := range llm.prompts {
		if got := tokensOf(p); got > budget {
			t.Fatalf("summary prompt %d is ~%d tokens, budget is %d", i, got, budget)
		}
	}
	if !strings.Contains(llm.prompts[0], "goal: fix the parser") {
		t.Fatal("fitting the prompt dropped the user's goal")
	}
	if !strings.Contains(strings.Join(llm.prompts, ""), "f5.go") {
		t.Fatal("fitting the prompt dropped the record of which tools ran")
	}
}

func TestCompactHistoryRetriesSmallerWhenTheSummaryOverflows(t *testing.T) {
	// The backend tokenizes denser than byte/4, so a prompt the estimate says
	// fits can still overflow. The backend's own figures say by how much, and
	// the head is then summarized in chunks that fit.
	frag := []openai.ChatCompletionMessage{{Role: "user", Content: "goal"}}
	for i := range 6 {
		frag = append(frag, toolTurn(fmt.Sprintf("c%d", i), fmt.Sprintf("f%d.go", i), strings.Repeat("x", 8000))...)
	}
	frag = append(frag,
		openai.ChatCompletionMessage{Role: "user", Content: "u2"},
		openai.ChatCompletionMessage{Role: "assistant", Content: "a2"},
	)
	orig := cloneMessages(frag)
	llm := &promptCapturingLLM{limit: 1500}
	s := newCompactTestSession(llm, 2, frag, frag)
	s.compaction.MaxContextTokens = 8000

	if _, _, err := s.CompactHistory(); err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}
	if n := len(llm.prompts); n < 2 || n > 1+maxRollingChunks {
		t.Fatalf("summary calls = %d, want an overflow and then smaller chunks", n)
	}
	if got := tokensOf(llm.prompts[len(llm.prompts)-1]); got > 1500 {
		t.Fatalf("the accepted prompt is ~%d tokens, over the backend's 1500", got)
	}
	// The messages no chunk covered are kept verbatim instead.
	checkVerbatimTail(t, orig, s.fragment.Messages)
}

func TestCompactHistoryGivesUpWhenNoSummaryFits(t *testing.T) {
	// Not even the instruction fits, and the rejection carries no usable
	// figures: the loop halves until it runs out of attempts.
	frag := []openai.ChatCompletionMessage{
		{Role: "user", Content: strings.Repeat("u", 4000)},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
	}
	orig := cloneMessages(frag)
	llm := &promptCapturingLLM{limit: 10}
	s := newCompactTestSession(llm, 1, frag, frag)
	s.compaction.MaxContextTokens = 1 << 20

	_, _, err := s.CompactHistory()
	if !isContextOverflow(errors.Unwrap(err)) {
		t.Fatalf("want the summarizer's overflow, got %v", err)
	}
	if len(llm.prompts) != maxSummaryAttempts {
		t.Fatalf("summary calls = %d, want exactly %d", len(llm.prompts), maxSummaryAttempts)
	}
	if !reflect.DeepEqual(s.fragment.Messages, orig) {
		t.Fatal("a failed compaction changed the fragment")
	}
}
