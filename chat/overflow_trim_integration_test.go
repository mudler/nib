package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
	openai "github.com/sashabaranov/go-openai"
)

// withOverflowTrim replaces the iterativeTrim step of overflow recovery for
// one test, so the basicCompact fallback can be reached on demand.
func withOverflowTrim(t *testing.T, f func(*Session, context.Context) error) {
	t.Helper()
	prev := overflowTrim
	overflowTrim = f
	t.Cleanup(func() { overflowTrim = prev })
}

// bigHistorySession is newOverflowSession with a history that basicCompact can
// shrink: short user messages and long assistant replies, which it elides.
func bigHistorySession(t *testing.T, llm cogito.LLM) *Session {
	t.Helper()
	s := newOverflowSession(t, llm)
	long := strings.Repeat("assistant detail ", 200)
	s.fragment = cogito.NewFragment(
		openai.ChatCompletionMessage{Role: "user", Content: "u1"},
		openai.ChatCompletionMessage{Role: "assistant", Content: "a1 " + long},
		openai.ChatCompletionMessage{Role: "user", Content: "u2"},
		openai.ChatCompletionMessage{Role: "assistant", Content: "a2 " + long},
		openai.ChatCompletionMessage{Role: "user", Content: "u3"},
		openai.ChatCompletionMessage{Role: "assistant", Content: "a3 " + long},
	)
	return s
}

func (r *retryStatusRecorder) saw(sub string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func fragmentHas(s *Session, pred func(openai.ChatCompletionMessage) bool) bool {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	for _, m := range s.fragment.Messages {
		if pred(m) {
			return true
		}
	}
	return false
}

// Step 1 of iterativeTrim (LLM compaction) is enough: the turn retries.
func TestOverflowChainTrimSucceedsAtStepOne(t *testing.T) {
	rec := &retryStatusRecorder{}
	llm := &overflowLLM{failures: 1}
	s := newOverflowSession(t, llm)
	s.callbacks = rec.callbacks()

	if _, err := s.SendMessage("what changed?"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if s.overflowRetries() != 1 {
		t.Fatalf("overflow retries = %d, want 1", s.overflowRetries())
	}
	llm.mu.Lock()
	asks := llm.asks
	llm.mu.Unlock()
	if asks == 0 {
		t.Fatal("no summary was requested: the retry did not come from LLM compaction")
	}
	if !rec.saw("compacting and retrying") || rec.saw("basic fallback") {
		t.Fatalf("want the compaction announcement, not the fallback one; statuses: %v", rec.lines)
	}
}

// iterativeTrim fails; basicCompact runs and succeeds; the turn retries with
// the fallback announcement.
func TestOverflowChainFallsBackToBasicCompact(t *testing.T) {
	var trims atomic.Int32
	withOverflowTrim(t, func(*Session, context.Context) error {
		trims.Add(1)
		return errors.New("iterative trim: nothing fits")
	})
	rec := &retryStatusRecorder{}
	s := bigHistorySession(t, &overflowLLM{failures: 1})
	s.callbacks = rec.callbacks()

	if _, err := s.SendMessage("what changed?"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if trims.Load() != 1 {
		t.Fatalf("iterativeTrim ran %d times, want 1", trims.Load())
	}
	if s.overflowRetries() != 1 {
		t.Fatalf("overflow retries = %d, want 1", s.overflowRetries())
	}
	if !rec.saw("using basic fallback compaction and retrying") {
		t.Fatalf("the fallback retry was not announced; statuses: %v", rec.lines)
	}
	if !fragmentHas(s, func(m openai.ChatCompletionMessage) bool { return strings.HasPrefix(m.Content, basicCompactMarker) }) {
		t.Fatal("the fragment carries no basic-compaction marker")
	}
	rec.mu.Lock()
	last := rec.lines[len(rec.lines)-1]
	rec.mu.Unlock()
	if last != retryResumeStatus {
		t.Fatalf("last status = %q, want %q", last, retryResumeStatus)
	}
}

// Both steps fail: the turn is rolled back and the overflow surfaces.
func TestOverflowChainBothFailRollsBack(t *testing.T) {
	s := newOverflowSession(t, &summaryFailingLLM{overflowLLM: overflowLLM{failures: 99}})
	tinyHistory(s)
	s.ensureSystemPrompt()
	before := append([]openai.ChatCompletionMessage(nil), s.fragment.Messages...)

	_, err := s.SendMessage("what changed?")
	if err == nil || !isContextOverflow(err) {
		t.Fatalf("err = %v, want the overflow", err)
	}
	if s.overflowRetries() != 0 {
		t.Fatalf("overflow retries = %d, want 0", s.overflowRetries())
	}
	got := s.fragment.Messages
	if len(got) != len(before) {
		t.Fatalf("fragment has %d messages, want the %d from before the turn", len(got), len(before))
	}
	for i := range got {
		if got[i].Role != before[i].Role || got[i].Content != before[i].Content {
			t.Fatalf("message %d changed: %+v", i, got[i])
		}
	}
}

// The whole chain is one recovery attempt.
func TestOverflowChainCountsAsOneRetry(t *testing.T) {
	var trims atomic.Int32
	withOverflowTrim(t, func(*Session, context.Context) error {
		trims.Add(1)
		return errors.New("iterative trim: nothing fits")
	})
	s := bigHistorySession(t, &overflowLLM{failures: 99})

	if _, err := s.SendMessage("what changed?"); err == nil {
		t.Fatal("expected the turn to fail")
	}
	if s.overflowRetries() != 1 {
		t.Fatalf("overflow retries = %d, want 1 for the whole chain", s.overflowRetries())
	}
	if trims.Load() != 1 {
		t.Fatalf("iterativeTrim ran %d times, want 1", trims.Load())
	}
}

// A cancelled turn never enters the chain.
func TestOverflowChainSkipsCancelledTurn(t *testing.T) {
	var trims atomic.Int32
	withOverflowTrim(t, func(*Session, context.Context) error {
		trims.Add(1)
		return nil
	})
	llm := &interruptingOverflowLLM{overflowLLM: overflowLLM{failures: 99}}
	s := newOverflowSession(t, llm)
	llm.s = s

	if _, err := s.SendMessage("what changed?"); err == nil {
		t.Fatal("expected the interrupted turn to fail")
	}
	if trims.Load() != 0 || s.overflowRetries() != 0 {
		t.Fatalf("trim ran %d times, retries = %d on a cancelled turn", trims.Load(), s.overflowRetries())
	}
}

// pairedOverflowLLM overflows every turn request until ok is set, and records
// the messages of every turn request.
type pairedOverflowLLM struct {
	overflowLLM
	ok   atomic.Bool
	reqs [][]openai.ChatCompletionMessage
}

func (p *pairedOverflowLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	if n := len(req.Messages); n > 0 && strings.HasPrefix(req.Messages[n-1].Content, compactInstruction) {
		return p.overflowLLM.CreateChatCompletion(ctx, req)
	}
	p.mu.Lock()
	p.reqs = append(p.reqs, append([]openai.ChatCompletionMessage(nil), req.Messages...))
	p.mu.Unlock()
	if p.ok.Load() {
		return replyWith("fine"), cogito.LLMUsage{PromptTokens: 10, TotalTokens: 10}, nil
	}
	return p.overflowLLM.CreateChatCompletion(ctx, req)
}

// Recovery compacts, the retry still overflows, the turn fails. The next turn
// starts from the compacted history, not the pre-turn one, with no orphans.
func TestOverflowFailedRetryKeepsTheCompaction(t *testing.T) {
	llm := &pairedOverflowLLM{overflowLLM: overflowLLM{failures: 1 << 20}}
	s := newOverflowSession(t, llm)
	s.fragment = cogito.NewFragment(
		openai.ChatCompletionMessage{Role: "user", Content: "u1"},
		openai.ChatCompletionMessage{Role: "assistant", ToolCalls: []openai.ToolCall{basicToolCall("c1", "read", `{"path":"/a"}`)}},
		openai.ChatCompletionMessage{Role: "tool", ToolCallID: "c1", Content: strings.Repeat("x", 4000)},
		openai.ChatCompletionMessage{Role: "assistant", Content: "a1"},
		openai.ChatCompletionMessage{Role: "user", Content: "u2"},
		openai.ChatCompletionMessage{Role: "assistant", ToolCalls: []openai.ToolCall{basicToolCall("c2", "read", `{"path":"/b"}`)}},
		openai.ChatCompletionMessage{Role: "tool", ToolCallID: "c2", Content: strings.Repeat("y", 4000)},
		openai.ChatCompletionMessage{Role: "assistant", Content: "a2"},
	)

	if _, err := s.SendMessage("what changed?"); err == nil {
		t.Fatal("expected the turn to fail")
	}
	if s.overflowRetries() != 1 {
		t.Fatalf("overflow retries = %d, want 1", s.overflowRetries())
	}
	isSummary := func(m openai.ChatCompletionMessage) bool {
		return strings.HasPrefix(m.Content, "[Earlier conversation compacted.")
	}
	if !fragmentHas(s, isSummary) {
		t.Fatalf("the failed turn was rolled back past its compaction (%d messages)", len(s.fragment.Messages))
	}
	if fragmentHas(s, func(m openai.ChatCompletionMessage) bool { return m.Role == "user" && m.Content == "what changed?" }) {
		t.Fatal("the failed turn's own message is still in the fragment")
	}
	if err := validateToolPairing(s.fragment.Messages); err != nil {
		t.Fatalf("pairing broken after the rollback: %v", err)
	}

	llm.ok.Store(true)
	if _, err := s.SendMessage("next"); err != nil {
		t.Fatalf("the next turn failed: %v", err)
	}
	llm.mu.Lock()
	next := llm.reqs[len(llm.reqs)-1]
	llm.mu.Unlock()
	sawSummary := false
	for _, m := range next {
		if isSummary(m) {
			sawSummary = true
		}
		if m.Content == strings.Repeat("x", 4000) {
			t.Fatal("the next turn sent the uncompacted history")
		}
	}
	if !sawSummary {
		t.Fatalf("the next turn did not start from the compacted history (%d messages)", len(next))
	}
	if err := validateToolPairing(next); err != nil {
		t.Fatalf("the next turn has orphaned tool messages: %v", err)
	}
}

// scriptedErrLLM fails its turn requests with errs, in order, then answers.
// Summary requests are counted apart and always succeed.
type scriptedErrLLM struct {
	mu        sync.Mutex
	errs      []error
	maxTokens []int
	summaries int
}

func (l *scriptedErrLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n := len(req.Messages); n > 0 && strings.HasPrefix(req.Messages[n-1].Content, compactInstruction) {
		l.summaries++
		return replyWith("summary"), cogito.LLMUsage{}, nil
	}
	l.maxTokens = append(l.maxTokens, req.MaxTokens)
	if len(l.errs) > 0 {
		err := l.errs[0]
		l.errs = l.errs[1:]
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}
	return replyWith("ok"), cogito.LLMUsage{PromptTokens: 10, TotalTokens: 10}, nil
}

func (l *scriptedErrLLM) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	l.mu.Lock()
	l.summaries++
	l.mu.Unlock()
	return f.AddMessage("assistant", "summary"), nil
}

func overflowFixtureErr(t *testing.T, name string) error {
	t.Helper()
	b, err := os.ReadFile("testdata/overflow/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Raw string `json:"raw"`
	}
	if err := json.Unmarshal(b, &fx); err != nil {
		t.Fatal(err)
	}
	return errors.New(fx.Raw)
}

// The regolo output-cap error lowers the cap and retries, without compacting.
func TestOverflowOutputCapLowersTheCap(t *testing.T) {
	llm := &scriptedErrLLM{errs: []error{overflowFixtureErr(t, "regolo-vllm-output-cap-nonstream")}}
	s := newOverflowSession(t, llm)
	s.outputCap = 250000
	s.compaction.MaxContextTokens = 400000

	if _, err := s.SendMessage("what changed?"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if cap, _ := s.requestLimits(); cap != 210000 {
		t.Fatalf("output cap = %d, want the stated 210000", cap)
	}
	if llm.summaries != 0 || s.overflowRetries() != 0 {
		t.Fatalf("an output-cap error compacted (summaries %d, retries %d)", llm.summaries, s.overflowRetries())
	}
	if len(llm.maxTokens) != 2 || llm.maxTokens[1] > 210000 {
		t.Fatalf("max_tokens per request = %v, want a retry at most 210000", llm.maxTokens)
	}

	// Never raised: an output-cap error stating more than the cap changes nothing.
	s.lowerOutputCap(300000, s.llmModel)
	if cap, _ := s.requestLimits(); cap != 210000 {
		t.Fatalf("output cap raised to %d", cap)
	}
}

// The regolo budget error retries with a lower max_tokens, without compacting.
func TestOverflowBudgetRetriesWithLowerMaxTokens(t *testing.T) {
	// The regolo capture, verbatim but for its input figure: vLLM counted 133
	// input tokens there, less than this session's tool schemas alone, so
	// the lowered reservation would not bind. 8000 keeps the shape (input
	// well under the window, prompt + reservation over it).
	raw := overflowFixtureErr(t, "regolo-vllm-budget-nonstream").Error()
	raw = strings.ReplaceAll(raw, "a total of 210123 tokens: 133 tokens", "a total of 217990 tokens: 8000 tokens")
	llm := &scriptedErrLLM{errs: []error{errors.New(raw)}}
	s := newOverflowSession(t, llm)
	s.outputCap = 209990
	s.compaction.MaxContextTokens = 210000
	before := append([]openai.ChatCompletionMessage(nil), s.fragment.Messages...)

	if _, err := s.SendMessage("what changed?"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if llm.summaries != 0 || s.overflowRetries() != 0 {
		t.Fatalf("a budget overflow compacted (summaries %d, retries %d)", llm.summaries, s.overflowRetries())
	}
	for i, m := range before {
		if s.fragment.Messages[i].Content != m.Content {
			t.Fatalf("message %d changed: %+v", i, s.fragment.Messages[i])
		}
	}
	// window − input − outputSafetyMargin, from the error's own figures.
	want := 210000 - 8000 - outputSafetyMargin
	if len(llm.maxTokens) != 2 || llm.maxTokens[1] != want || llm.maxTokens[1] >= llm.maxTokens[0] {
		t.Fatalf("max_tokens per request = %v, want a retry at %d", llm.maxTokens, want)
	}
	// One turn only.
	if cap, _ := s.requestLimits(); cap != 209990 {
		t.Fatalf("output cap after the turn = %d, want the session's 209990", cap)
	}
}

// Regression for the failure diagnosed on regolo (LiteLLM in front of vLLM).
//
// Scaled 1:10 to keep it fast: vLLM's window is 21000 (really 210000), and
// LiteLLM's /model/info reports 9600 input / 9600 output (really 96000). The
// shape is what matters: the LiteLLM window is smaller than vLLM's, and the
// output cap equals the LiteLLM window. The fake vLLM counts a prompt as
// bytes/3, not nib's bytes/4, the way a real tokenizer disagrees with nib's
// estimate, and rejects prompt + max_tokens > 21000 with vLLM's current
// message wrapped the way regolo wraps it.
//
// The session grows past 11400 tokens (window − cap, really 114000). Every
// turn must succeed or recover, and a manual /compact must succeed after an
// overflow.
func TestOverflowRegoloRegressionOverHTTP(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	const (
		vllmWindow = 21000
		litellmCap = 9600
	)
	var rejected, maxInput atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			if strings.HasSuffix(r.URL.Path, "/model/info") {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"data":[{"model_name":"m","model_info":{"max_input_tokens":%d,"max_output_tokens":%d}}]}`, litellmCap, litellmCap)
				return
			}
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Content any `json:"content"`
			} `json:"messages"`
			Tools               json.RawMessage `json:"tools"`
			MaxTokens           int             `json:"max_tokens"`
			MaxCompletionTokens int             `json:"max_completion_tokens"`
		}
		_ = json.Unmarshal(body, &req)
		bytes, last := 0, ""
		for _, m := range req.Messages {
			if c, ok := m.Content.(string); ok {
				bytes += len(c)
				last = c
			}
		}
		input := bytes/3 + len(req.Tools)/4
		output := max(req.MaxTokens, req.MaxCompletionTokens)
		if input+output > vllmWindow {
			rejected.Add(1)
			inner, _ := json.Marshal(map[string]any{
				"object": "error",
				"message": fmt.Sprintf("Requested token count exceeds the model's maximum context length of %d tokens. You requested a total of %d tokens: %d tokens from the input messages and %d tokens for the completion. Please reduce the number of tokens in the input messages or the completion to fit within the limit.",
					vllmWindow, input+output, input, output),
				"type": "BadRequestError", "param": nil, "code": 400,
			})
			wrapped := "regolo.BadRequestError: Hosted_vllmException - " + string(inner) +
				"No fallback model group found for original model_group=m.. Received Model Group=m\nAvailable Model Group Fallbacks=None\n" +
				"regolo.BadRequestError: Hosted_vllmException - " + string(inner) +
				"No fallback model group found for original model_group=m. Retried: 2 times"
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"message": wrapped, "type": nil, "param": nil, "code": "400"},
			})
			return
		}
		for {
			old := maxInput.Load()
			if int64(input) <= old || maxInput.CompareAndSwap(old, int64(input)) {
				break
			}
		}
		reply := "ok"
		if strings.HasPrefix(last, compactInstruction) {
			reply = "summary of the earlier turns"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "cmpl-1", "object": "chat.completion", "created": 1, "model": "m",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": reply},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": input, "completion_tokens": 1, "total_tokens": input + 1},
		})
	}))
	defer srv.Close()

	cfg := types.Config{
		Model:        "m",
		APIKey:       "k",
		BaseURL:      srv.URL + "/v1",
		LogLevel:     "error",
		ApprovalMode: "auto",
		BuiltinTools: []string{"read"},
		// MaxRetries 1: see TestOverflowRecoveryOverHTTP.
		AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 1},
		// MaxContextTokens 0: the window is discovered from /model/info.
		Compaction: types.CompactionConfig{Threshold: 0.8, KeepRecent: 2, ReserveTokens: 1024},
	}
	s, err := NewSession(context.Background(), cfg, Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	send := func(i, size int) error {
		_, err := s.SendMessage(fmt.Sprintf("turn %d: ", i) + strings.Repeat("w", size))
		return err
	}
	// Ordinary turns, budgeted against LiteLLM's window.
	turn := 0
	for ; turn < 3; turn++ {
		if err := send(turn, 6000); err != nil {
			t.Fatalf("turn %d got stuck: %v", turn, err)
		}
	}
	// A paste larger than vLLM's real window. No recovery can make it fit,
	// and with the reservation clamped this is the only way a session
	// budgeted against LiteLLM's window meets vLLM's. The turn fails with
	// the overflow, teaches nib the real window, and must not poison the
	// session.
	if err := send(turn, 3*(vllmWindow+500)); err == nil || !isContextOverflow(err) {
		t.Fatalf("the oversized paste: err = %v, want the overflow", err)
	}
	turn++
	if got := s.contextWindow(); got != vllmWindow {
		t.Fatalf("contextWindow = %d, want the learned %d", got, vllmWindow)
	}
	// Now the session grows past window − cap against the learned window.
	// Every turn must succeed.
	for end := turn + 12; turn < end; turn++ {
		if err := send(turn, 6000); err != nil {
			t.Fatalf("turn %d got stuck: %v", turn, err)
		}
	}
	if rejected.Load() < 2 {
		t.Fatalf("the backend rejected %d requests: the regression was not exercised", rejected.Load())
	}
	if maxInput.Load() <= vllmWindow-litellmCap {
		t.Fatalf("largest accepted prompt = %d tokens; the session never grew past %d", maxInput.Load(), vllmWindow-litellmCap)
	}
	if _, _, err := s.CompactHistory(); err != nil {
		t.Fatalf("manual /compact failed after an overflow: %v", err)
	}
}
