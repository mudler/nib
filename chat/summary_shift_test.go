package chat

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/mcp"
	openai "github.com/sashabaranov/go-openai"
)

// vllmOverflow is vLLM's current rejection of a request whose prompt plus the
// output it reserves exceeds the window.
func vllmOverflow(window, output, prompt int) error {
	return fmt.Errorf("This model's maximum context length is %d tokens. However, you requested %d output tokens and your prompt contains %d input tokens, for a total of %d tokens. Please reduce the length of the input prompt or the number of requested output tokens.",
		window, output, prompt, prompt+output)
}

// reservingLLM is a backend that checks the output reservation the way vLLM
// does: it rejects a request when prompt + max_tokens exceeds limit, whatever
// the model would actually generate. The prompt is counted byte/4.
type reservingLLM struct {
	// limit is the backend's window. 0 accepts every size.
	limit int
	// overBy, when positive, rejects every request as overflowing by that
	// many tokens.
	overBy int
	// err, when set, is returned for every request.
	err  error
	reqs []openai.ChatCompletionRequest
}

func (f *reservingLLM) Ask(ctx context.Context, fr cogito.Fragment) (cogito.Fragment, error) {
	return cogito.Fragment{}, errors.New("the summary must be sent with CreateChatCompletion, not Ask")
}

func (f *reservingLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	f.reqs = append(f.reqs, req)
	usage := cogito.LLMUsage{PromptTokens: 1, TotalTokens: 1}
	if f.err != nil {
		return cogito.LLMReply{}, usage, f.err
	}
	p := tokensOf(req.Messages[len(req.Messages)-1].Content)
	if f.overBy > 0 {
		return cogito.LLMReply{}, usage, vllmOverflow(p+req.MaxTokens-f.overBy, req.MaxTokens, p)
	}
	if f.limit > 0 && p+req.MaxTokens > f.limit {
		return cogito.LLMReply{}, usage, vllmOverflow(f.limit, req.MaxTokens, p)
	}
	return cogito.LLMReply{ChatCompletionResponse: openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Role: "assistant", Content: "SUMMARY"}}},
	}}, usage, nil
}

// longHistory is a goal, n tool steps of ~2000 tokens each, and a short
// closing exchange.
func longHistory(n int) []openai.ChatCompletionMessage {
	msgs := []openai.ChatCompletionMessage{{Role: "user", Content: "goal: fix the parser"}}
	for i := range n {
		msgs = append(msgs, toolTurn(fmt.Sprintf("c%d", i), fmt.Sprintf("f%d.go", i), fmt.Sprintf("BODY%02d", i)+strings.Repeat("x", 8000))...)
	}
	return append(msgs,
		openai.ChatCompletionMessage{Role: "user", Content: "u2"},
		openai.ChatCompletionMessage{Role: "assistant", Content: "a2"},
	)
}

func cloneMessages(msgs []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	return append([]openai.ChatCompletionMessage(nil), msgs...)
}

// checkVerbatimTail asserts that got is [summary] followed by a verbatim
// suffix of orig that starts on a clean boundary, and returns how many of
// orig's messages the summary stands for.
func checkVerbatimTail(t *testing.T, orig, got []openai.ChatCompletionMessage) int {
	t.Helper()
	if len(got) < 2 || !strings.Contains(got[0].Content, "SUMMARY") {
		t.Fatalf("want the summary followed by the kept messages, got %d messages", len(got))
	}
	kept := got[1:]
	start := len(orig) - len(kept)
	if start < 1 {
		t.Fatalf("kept %d of %d messages", len(kept), len(orig))
	}
	if !reflect.DeepEqual(kept, orig[start:]) {
		t.Fatal("the kept messages are not a verbatim, in-order suffix of the history")
	}
	if kept[0].Role == "tool" || len(orig[start-1].ToolCalls) > 0 {
		t.Fatal("the boundary separates a tool call from its result")
	}
	return start
}

func TestSummaryShiftConvergesWithAFixedOutputReservation(t *testing.T) {
	// The session believes the window is huge, the backend serves 8192 and
	// reserves the full 4000 summary tokens on every request. Scaling the
	// prompt by allows/needs does not converge: the reservation does not
	// scale with it.
	frag := longHistory(20)
	orig := cloneMessages(frag)
	llm := &reservingLLM{limit: 8192}
	s := newCompactTestSession(llm, 2, frag, frag)
	s.compaction.MaxContextTokens = 1 << 20
	s.compaction.SummaryMaxTokens = 4000

	if _, _, err := s.CompactHistory(); err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}
	if n := len(llm.reqs); n < 2 || n > maxSummaryAttempts {
		t.Fatalf("summary calls = %d, want an overflow then a fitting retry", n)
	}
	for i, r := range llm.reqs {
		if r.MaxTokens != 4000 {
			t.Fatalf("request %d MaxTokens = %d, want SummaryMaxTokens 4000", i, r.MaxTokens)
		}
	}
	start := checkVerbatimTail(t, orig, s.fragment.Messages)
	if len(orig)-start <= 2 {
		t.Fatal("the boundary did not move: the kept tail is still KeepRecent long")
	}
	if !strings.Contains(llm.reqs[len(llm.reqs)-1].Messages[0].Content, "goal: fix the parser") {
		t.Fatal("the shifted head lost the user's goal")
	}
}

func TestSummaryRequestCarriesTheSummaryOutputCap(t *testing.T) {
	for _, tc := range []struct {
		name           string
		cfg, outputCap int
		wantMaxTokens  int
	}{
		{"configured", 1234, 0, 1234},
		{"default", 0, 0, 16384},
		{"model cap is smaller", 1234, 700, 700},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frag := longHistory(2)
			llm := &reservingLLM{}
			s := newCompactTestSession(llm, 2, frag, frag)
			s.compaction.MaxContextTokens = 1 << 20
			s.compaction.SummaryMaxTokens = tc.cfg
			s.outputCap = tc.outputCap
			if _, _, err := s.CompactHistory(); err != nil {
				t.Fatalf("CompactHistory: %v", err)
			}
			if len(llm.reqs) != 1 || llm.reqs[0].MaxTokens != tc.wantMaxTokens {
				t.Fatalf("requests = %d, MaxTokens = %d, want one with %d", len(llm.reqs), llm.reqs[0].MaxTokens, tc.wantMaxTokens)
			}
		})
	}
}

func TestSummaryGivesUpAfterMaxAttempts(t *testing.T) {
	frag := longHistory(6)
	orig := cloneMessages(frag)
	llm := &reservingLLM{overBy: 50}
	s := newCompactTestSession(llm, 2, frag, frag)
	s.compaction.MaxContextTokens = 1 << 20
	s.compaction.SummaryMaxTokens = 1000
	s.artifacts = mcp.NewArtifactStore()

	_, _, err := s.CompactHistory()
	if !isContextOverflow(err) {
		t.Fatalf("want the last overflow, got %v", err)
	}
	if len(llm.reqs) != maxSummaryAttempts {
		t.Fatalf("summary calls = %d, want %d", len(llm.reqs), maxSummaryAttempts)
	}
	if !reflect.DeepEqual(s.fragment.Messages, orig) {
		t.Fatal("a failed compaction changed the fragment")
	}
	if s.artifacts.Count() != 0 {
		t.Fatal("a failed compaction spilled an artifact")
	}
}

func TestSummaryStopsOnANonOverflowError(t *testing.T) {
	frag := longHistory(6)
	orig := cloneMessages(frag)
	llm := &reservingLLM{err: errors.New("status code: 400, bad request")}
	s := newCompactTestSession(llm, 2, frag, frag)
	s.compaction.MaxContextTokens = 1 << 20

	if _, _, err := s.CompactHistory(); err == nil {
		t.Fatal("want the backend error")
	}
	if len(llm.reqs) != 1 {
		t.Fatalf("summary calls = %d, want 1", len(llm.reqs))
	}
	if !reflect.DeepEqual(s.fragment.Messages, orig) {
		t.Fatal("a failed compaction changed the fragment")
	}
}

func TestSummaryStopsWhenTheReservationAloneExceedsTheWindow(t *testing.T) {
	frag := longHistory(6)
	llm := &reservingLLM{limit: 8192}
	s := newCompactTestSession(llm, 2, frag, frag)
	s.compaction.MaxContextTokens = 1 << 20
	s.compaction.SummaryMaxTokens = 10000

	if _, _, err := s.CompactHistory(); !isContextOverflow(err) {
		t.Fatalf("want the overflow, got %v", err)
	}
	if len(llm.reqs) != 1 {
		t.Fatalf("summary calls = %d, want 1: no prompt fits beside a 10000-token reservation in 8192", len(llm.reqs))
	}
}

func TestSummaryArtifactHoldsExactlyTheSummarizedHead(t *testing.T) {
	frag := longHistory(20)
	orig := cloneMessages(frag)
	llm := &reservingLLM{limit: 8192}
	s := newCompactTestSession(llm, 2, frag, frag)
	s.compaction.MaxContextTokens = 1 << 20
	s.compaction.SummaryMaxTokens = 4000
	s.artifacts = mcp.NewArtifactStore()

	if _, _, err := s.CompactHistory(); err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}
	start := checkVerbatimTail(t, orig, s.fragment.Messages)
	if s.artifacts.Count() != 1 {
		t.Fatalf("artifacts = %d, want 1", s.artifacts.Count())
	}
	var want strings.Builder
	for _, p := range renderMessages(orig[:start]) {
		want.WriteString(p.text)
	}
	if got := s.artifacts.Get(1).Content; got != want.String() {
		t.Fatalf("artifact is %d bytes, want exactly the %d-byte summarized head", len(got), want.Len())
	}
}

func TestMidTurnCompactionShiftsTheBoundaryWhenTheSummaryOverflows(t *testing.T) {
	msgs := longHistory(20)
	llm := &reservingLLM{limit: 8192}
	s := newCompactTestSession(llm, 2, msgs, msgs)
	s.compaction.MaxContextTokens = 1 << 20
	s.compaction.SummaryMaxTokens = 4000
	c := s.newTurnCompactor(context.Background())

	out := c.compact(msgs, msgs, 0, 0, 0)
	if len(llm.reqs) < 2 {
		t.Fatalf("summary calls = %d, want the loop to retry", len(llm.reqs))
	}
	start := checkVerbatimTail(t, msgs, out)
	if len(msgs)-start <= 2 {
		t.Fatal("the boundary did not move")
	}
	if c.covered != start {
		t.Fatalf("covered = %d, want %d", c.covered, start)
	}
}

func TestMidTurnCompactionSkipsAHeadOfOnlyThePreviousSummary(t *testing.T) {
	raw := longHistory(5)
	// The request already carries a summary in place of the first 3 messages.
	prev := openai.ChatCompletionMessage{Role: "user", Content: "PREV"}
	out := append([]openai.ChatCompletionMessage{prev}, cloneMessages(raw[3:])...)
	llm := &reservingLLM{limit: 3000}
	s := newCompactTestSession(llm, 2, raw, raw)
	s.compaction.MaxContextTokens = 1 << 20
	s.compaction.SummaryMaxTokens = 1000
	c := s.newTurnCompactor(context.Background())

	got := c.compact(raw, out, 3, 1, 0)
	if len(llm.reqs) < 2 {
		t.Fatalf("summary calls = %d, want the loop to shrink the head to the previous summary", len(llm.reqs))
	}
	if !reflect.DeepEqual(got, out) {
		t.Fatal("a head of only the previous summary should leave the request unchanged")
	}
	if c.covered != 0 {
		t.Fatalf("covered = %d, want 0", c.covered)
	}
}
