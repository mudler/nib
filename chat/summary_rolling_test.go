package chat

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/mcp"
	"github.com/mudler/nib/types"
	openai "github.com/sashabaranov/go-openai"
)

// rollingLLM is a backend that checks the output reservation the way vLLM
// does (prompt + max_tokens against limit, prompt counted byte/4), and
// numbers its summaries SUMMARY-1, SUMMARY-2, ... so a test can see which
// summary a later prompt carries.
type rollingLLM struct {
	limit int
	// overflowCall, when positive, rejects that call (1-based) as
	// overflowing by overBy tokens, whatever its size.
	overflowCall, overBy int
	reqs                 []openai.ChatCompletionRequest
	served               int
}

func (f *rollingLLM) Ask(ctx context.Context, fr cogito.Fragment) (cogito.Fragment, error) {
	return cogito.Fragment{}, fmt.Errorf("the summary must be sent with CreateChatCompletion, not Ask")
}

func (f *rollingLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	f.reqs = append(f.reqs, req)
	usage := cogito.LLMUsage{PromptTokens: 10, CompletionTokens: 1, TotalTokens: 11}
	p := tokensOf(req.Messages[len(req.Messages)-1].Content)
	if len(f.reqs) == f.overflowCall {
		return cogito.LLMReply{}, usage, vllmOverflow(p+req.MaxTokens-f.overBy, req.MaxTokens, p)
	}
	if f.limit > 0 && p+req.MaxTokens > f.limit {
		return cogito.LLMReply{}, usage, vllmOverflow(f.limit, req.MaxTokens, p)
	}
	f.served++
	return replyWith(fmt.Sprintf("SUMMARY-%d", f.served)), usage, nil
}

func (f *rollingLLM) prompt(i int) string { return f.reqs[i].Messages[0].Content }

// shortHistory is a goal, n tool steps of ~500 tokens each, and a short
// closing exchange.
func shortHistory(n int) []openai.ChatCompletionMessage {
	msgs := []openai.ChatCompletionMessage{{Role: "user", Content: "goal: fix the parser"}}
	for i := range n {
		msgs = append(msgs, toolTurn(fmt.Sprintf("c%d", i), fmt.Sprintf("f%d.go", i), fmt.Sprintf("BODY%02d", i)+strings.Repeat("x", 2000))...)
	}
	return append(msgs,
		openai.ChatCompletionMessage{Role: "user", Content: "u2"},
		openai.ChatCompletionMessage{Role: "assistant", Content: "a2"},
	)
}

// rollingSession is a session that believes the window is huge, while the
// backend serves 8192 and every summary reserves 4000 of it.
func rollingSession(llm cogito.LLM, frag []openai.ChatCompletionMessage) *Session {
	s := newCompactTestSession(llm, 2, frag, frag)
	s.compaction.MaxContextTokens = 1 << 20
	s.compaction.SummaryMaxTokens = 4000
	s.artifacts = mcp.NewArtifactStore()
	return s
}

// chunkPrompts are the prompts of the rolling chunks that the backend
// served, in order: the first, whole-head attempt is left out.
func servedChunkPrompts(llm *rollingLLM) []string {
	var out []string
	for i := 1; i < len(llm.reqs); i++ {
		if i+1 == llm.overflowCall {
			continue
		}
		out = append(out, llm.prompt(i))
	}
	return out
}

func TestRollingSummaryCoversTheWholeHeadInChunks(t *testing.T) {
	frag := shortHistory(24) // ~13k tokens against a ~3.7k fitting size
	orig := cloneMessages(frag)
	llm := &rollingLLM{limit: 8192}
	s := rollingSession(llm, frag)

	if _, _, err := s.CompactHistory(); err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}
	chunks := len(llm.reqs) - 1
	if chunks < 3 || chunks > maxRollingChunks {
		t.Fatalf("chunks = %d, want the head covered in several chunks", chunks)
	}
	got := s.fragment.Messages
	if len(got) != 3 || got[0].Content == "" || !strings.Contains(got[0].Content, fmt.Sprintf("SUMMARY-%d", chunks)) {
		t.Fatalf("want [final summary] + the original tail, got %d messages", len(got))
	}
	if !reflect.DeepEqual(got[1:], orig[len(orig)-2:]) {
		t.Fatal("the tail is not the original KeepRecent tail")
	}
	real := ContextBudget(types.CompactionConfig{}, 8192)
	if n := estimateTokens(got); n >= real {
		t.Fatalf("compacted history is %d tokens, want below the real budget %d", n, real)
	}
	for i, r := range llm.reqs {
		if r.MaxTokens != 4000 {
			t.Fatalf("request %d MaxTokens = %d, want 4000", i, r.MaxTokens)
		}
	}
	// Every message of the head reached a chunk exactly once.
	all := strings.Join(servedChunkPrompts(llm), "")
	for i := range 24 {
		if c := strings.Count(all, fmt.Sprintf("BODY%02d", i)); c != 1 {
			t.Fatalf("BODY%02d appears in %d chunks, want 1", i, c)
		}
	}
	if !strings.Contains(llm.prompt(1), "goal: fix the parser") {
		t.Fatal("the first chunk lost the user's goal")
	}
}

func TestRollingSummaryChunksCarryTheRunningSummaryFirst(t *testing.T) {
	llm := &rollingLLM{limit: 8192}
	s := rollingSession(llm, shortHistory(24))
	if _, _, err := s.CompactHistory(); err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}
	prompts := servedChunkPrompts(llm)
	if len(prompts) < 2 {
		t.Fatalf("chunks = %d, want several", len(prompts))
	}
	if !strings.HasPrefix(prompts[0], summaryPrefix) {
		t.Fatal("the first chunk must use the normal compaction instruction")
	}
	for k := 1; k < len(prompts); k++ {
		p := prompts[k]
		if !strings.HasPrefix(p, rollingInstruction) {
			t.Fatalf("chunk %d does not use rollingInstruction", k+1)
		}
		prev := fmt.Sprintf("SUMMARY-%d", k)
		at := strings.Index(p, prev)
		body := strings.Index(p, "BODY")
		if at < 0 || body < 0 || at > body {
			t.Fatalf("chunk %d does not carry %s before its messages", k+1, prev)
		}
	}
}

func TestRollingChunkBoundariesKeepToolCallsWithResults(t *testing.T) {
	llm := &rollingLLM{limit: 8192}
	s := rollingSession(llm, shortHistory(24))
	if _, _, err := s.CompactHistory(); err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}
	for k, p := range servedChunkPrompts(llm) {
		calls := strings.Count(p, "[tool call read(")
		results := strings.Count(p, "\ntool: BODY")
		if calls != results {
			t.Fatalf("chunk %d has %d tool calls and %d results", k+1, calls, results)
		}
	}
}

func TestRollingChunkThatOverflowsIsShrunk(t *testing.T) {
	frag := shortHistory(24)
	orig := cloneMessages(frag)
	// The first chunk overflows on the backend anyway, by 400 tokens.
	llm := &rollingLLM{limit: 8192, overflowCall: 2, overBy: 400}
	s := rollingSession(llm, frag)

	if _, _, err := s.CompactHistory(); err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}
	if len(llm.reqs) < 4 {
		t.Fatalf("requests = %d, want the overflowing chunk retried", len(llm.reqs))
	}
	if tokensOf(llm.prompt(2)) >= tokensOf(llm.prompt(1)) {
		t.Fatalf("the retried chunk is %d tokens, want smaller than the %d that overflowed", tokensOf(llm.prompt(2)), tokensOf(llm.prompt(1)))
	}
	got := s.fragment.Messages
	if len(got) != 3 || !reflect.DeepEqual(got[1:], orig[len(orig)-2:]) {
		t.Fatalf("want [summary] + original tail, got %d messages", len(got))
	}
	all := strings.Join(servedChunkPrompts(llm), "")
	for i := range 24 {
		if c := strings.Count(all, fmt.Sprintf("BODY%02d", i)); c != 1 {
			t.Fatalf("BODY%02d appears in %d served chunks, want 1: the rest must move to the next chunk", i, c)
		}
	}
}

func TestRollingSummaryStopsAfterMaxRollingChunks(t *testing.T) {
	frag := longHistory(20) // one ~2000-token step per chunk
	orig := cloneMessages(frag)
	llm := &rollingLLM{limit: 8192}
	s := rollingSession(llm, frag)

	if _, _, err := s.CompactHistory(); err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}
	if n := len(llm.reqs); n != 1+maxRollingChunks {
		t.Fatalf("requests = %d, want the first attempt and %d chunks", n, maxRollingChunks)
	}
	start := checkVerbatimTail(t, orig, s.fragment.Messages)
	if len(orig)-start <= 2 {
		t.Fatal("want the messages no chunk covered kept verbatim")
	}
	// The final summary stands for exactly the covered messages.
	if s.artifacts.Count() != 1 {
		t.Fatalf("artifacts = %d, want 1", s.artifacts.Count())
	}
	var want strings.Builder
	for _, p := range renderMessages(orig[:start]) {
		want.WriteString(p.text)
	}
	if got := s.artifacts.Get(1).Content; got != want.String() {
		t.Fatalf("artifact is %d bytes, want exactly the %d covered bytes", len(got), want.Len())
	}
	all := strings.Join(servedChunkPrompts(llm), "")
	if !strings.Contains(all, orig[start-1].Content) {
		t.Fatal("the last covered message is not in any chunk")
	}
	if strings.Contains(all, orig[start+1].Content) {
		t.Fatal("a message kept verbatim was also summarized")
	}
}

func TestRollingSummaryCountsEveryChunkCall(t *testing.T) {
	llm := &rollingLLM{limit: 8192, overflowCall: 3, overBy: 500}
	s := rollingSession(llm, shortHistory(24))
	if _, _, err := s.CompactHistory(); err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}
	if got, want := s.Usage().TotalTokens, 11*len(llm.reqs); got != want {
		t.Fatalf("TotalTokens = %d, want %d for %d calls", got, want, len(llm.reqs))
	}
}

func TestRollingArtifactHoldsExactlyTheCoveredHead(t *testing.T) {
	frag := shortHistory(24)
	orig := cloneMessages(frag)
	llm := &rollingLLM{limit: 8192}
	s := rollingSession(llm, frag)
	if _, _, err := s.CompactHistory(); err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}
	var want strings.Builder
	for _, p := range renderMessages(orig[:len(orig)-2]) {
		want.WriteString(p.text)
	}
	if s.artifacts.Count() != 1 || s.artifacts.Get(1).Content != want.String() {
		t.Fatal("the artifact must hold exactly the whole covered head")
	}
}

func TestRollingSummaryCutsAMessageLargerThanTheTarget(t *testing.T) {
	frag := shortHistory(4)
	// One result alone is far larger than any prompt the backend accepts.
	frag[4].Content = "HUGE" + strings.Repeat("y", 40000)
	orig := cloneMessages(frag)
	llm := &rollingLLM{limit: 8192}
	s := rollingSession(llm, frag)

	if _, _, err := s.CompactHistory(); err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}
	got := s.fragment.Messages
	if len(got) != 3 || !reflect.DeepEqual(got[1:], orig[len(orig)-2:]) {
		t.Fatalf("want the whole head covered, got %d messages", len(got))
	}
	all := strings.Join(servedChunkPrompts(llm), "")
	if !strings.Contains(all, "tool: HUGE") || !strings.Contains(all, "omitted to fit the summary") {
		t.Fatal("the over-large message must be cut down, not skipped")
	}
	if !strings.Contains(s.artifacts.Get(1).Content, frag[4].Content) {
		t.Fatal("the artifact must hold the over-large message in full")
	}
}

// knownWindowSession is a session that knows the backend's 8192-token window,
// where every summary reserves 4000 of it.
func knownWindowSession(llm cogito.LLM, frag []openai.ChatCompletionMessage) *Session {
	s := newCompactTestSession(llm, 2, frag, frag)
	s.compaction.MaxContextTokens = 8192
	s.compaction.SummaryMaxTokens = 4000
	s.artifacts = mcp.NewArtifactStore()
	return s
}

const (
	cutMarker     = "bytes omitted to fit the summary"
	droppedMarker = "earlier messages omitted to fit the summary"
)

func TestRollingStartsAtOnceForAHeadKnownToBeTooLarge(t *testing.T) {
	frag := shortHistory(24) // ~13k tokens against a ~3.7k target
	orig := cloneMessages(frag)
	llm := &rollingLLM{limit: 8192}
	s := knownWindowSession(llm, frag)

	if _, _, err := s.CompactHistory(); err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}
	if len(llm.reqs) < 3 || llm.served != len(llm.reqs) {
		t.Fatalf("requests = %d, served = %d: want several chunks and no overflow", len(llm.reqs), llm.served)
	}
	for i := range llm.reqs {
		p := llm.prompt(i)
		if strings.Contains(p, "BODY00") && strings.Contains(p, "BODY23") {
			t.Fatalf("request %d carries the whole head", i)
		}
		if strings.Contains(p, cutMarker) || strings.Contains(p, droppedMarker) {
			t.Fatalf("request %d was cut by fitSummaryInput", i)
		}
	}
	all := ""
	for i := range llm.reqs {
		all += llm.prompt(i)
	}
	for i := range 24 {
		if c := strings.Count(all, fmt.Sprintf("BODY%02d", i)); c != 1 {
			t.Fatalf("BODY%02d appears in %d chunks, want 1", i, c)
		}
	}
	got := s.fragment.Messages
	if len(got) != 3 || !strings.Contains(got[0].Content, fmt.Sprintf("SUMMARY-%d", len(llm.reqs))) {
		t.Fatalf("want [final summary] + the original tail, got %d messages", len(got))
	}
	if !reflect.DeepEqual(got[1:], orig[len(orig)-2:]) {
		t.Fatal("the tail is not the original KeepRecent tail")
	}
}

func TestRollingNotUsedForAHeadThatFits(t *testing.T) {
	frag := shortHistory(2)
	llm := &rollingLLM{limit: 8192}
	s := knownWindowSession(llm, frag)
	if _, _, err := s.CompactHistory(); err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}
	if len(llm.reqs) != 1 {
		t.Fatalf("summary calls = %d, want 1", len(llm.reqs))
	}
	if !strings.HasPrefix(llm.prompt(0), summaryPrefix) || !strings.Contains(llm.prompt(0), "BODY01") {
		t.Fatal("want the whole head in one normal summary call")
	}
}

func TestRollingWithAnUnknownWindowStartsOnOverflow(t *testing.T) {
	frag := shortHistory(24)
	llm := &rollingLLM{limit: 8192}
	s := knownWindowSession(llm, frag)
	s.compaction.MaxContextTokens = 0

	if _, _, err := s.CompactHistory(); err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}
	if len(llm.reqs) < 3 || llm.served != len(llm.reqs)-1 {
		t.Fatalf("requests = %d, served = %d: want an overflow then chunks", len(llm.reqs), llm.served)
	}
	first := llm.prompt(0)
	if !strings.Contains(first, "BODY00") || !strings.Contains(first, "BODY23") {
		t.Fatal("the first request must carry the whole head")
	}
}

func TestRollingProactiveCutsOnlyThePieceLargerThanTheTarget(t *testing.T) {
	frag := shortHistory(24)
	// One result alone is far larger than the target.
	frag[4].Content = "HUGE" + strings.Repeat("y", 40000)
	orig := cloneMessages(frag)
	llm := &rollingLLM{limit: 8192}
	s := knownWindowSession(llm, frag)

	if _, _, err := s.CompactHistory(); err != nil {
		t.Fatalf("CompactHistory: %v", err)
	}
	if llm.served != len(llm.reqs) {
		t.Fatalf("requests = %d, served = %d: want no overflow", len(llm.reqs), llm.served)
	}
	cuts := 0
	for i := range llm.reqs {
		p := llm.prompt(i)
		if strings.Contains(p, droppedMarker) {
			t.Fatalf("request %d dropped whole messages", i)
		}
		if n := strings.Count(p, cutMarker); n > 0 {
			cuts += n
			if !strings.Contains(p, "tool: HUGE") {
				t.Fatalf("request %d cut a piece other than the huge one", i)
			}
		}
	}
	if cuts != 1 {
		t.Fatalf("cuts = %d, want exactly the one huge piece cut", cuts)
	}
	got := s.fragment.Messages
	if len(got) != 3 || !reflect.DeepEqual(got[1:], orig[len(orig)-2:]) {
		t.Fatalf("want [summary] + the original tail, got %d messages", len(got))
	}
	if !strings.Contains(s.artifacts.Get(1).Content, frag[4].Content) {
		t.Fatal("the artifact must hold the over-large message in full")
	}
}
