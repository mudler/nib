package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
	openai "github.com/sashabaranov/go-openai"
)

// The fixture's window: a 20000 window less a 1000 reserve is a 19000 budget,
// and the default 0.8 threshold puts the auto-compaction trigger at 15200.
const (
	midTurnWindow  = 20000
	midTurnReserve = 1000
	midTurnTrigger = 15200
	midTurnPrompt  = "you are nib MIDTURN_SYSTEM_PROMPT"
)

// midTurnRequest is one request the fake backend saw, in arrival order.
type midTurnRequest struct {
	summary bool
	msgs    []openai.ChatCompletionMessage
}

// tokens is the byte/4 size of the request's messages, the same estimate the
// session measures with.
func (r midTurnRequest) tokens() int {
	return estimateTokens(r.msgs)
}

func (r midTurnRequest) contains(s string) bool {
	for _, m := range r.msgs {
		if strings.Contains(m.Content, s) {
			return true
		}
	}
	return false
}

// midTurnBackend scripts a multi-step turn: the first len(steps) turn requests
// are answered with an ask_user call, the next with a plain "done". A request
// whose only message is the compaction prompt is a summary and gets a numbered
// summary back, so the test can tell which summary a later request carries.
type midTurnBackend struct {
	mu        sync.Mutex
	reqs      []midTurnRequest
	steps     int
	summaries int
	turnReqs  int
	// extra is added to the byte/4 size of a turn request to make up the
	// prompt tokens reported for it, standing in for tool schemas and tokenizer
	// skew. Zero reports a fixed small figure.
	extra int
}

func (b *midTurnBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if isModelProbe(r) {
		serveEmptyModels(w)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var req openai.ChatCompletionRequest
	_ = json.Unmarshal(body, &req)

	summary := len(req.Messages) == 1 && strings.HasPrefix(req.Messages[0].Content, "You are compacting")

	promptTokens := 10
	if b.extra > 0 && !summary {
		promptTokens = estimateTokens(req.Messages) + b.extra
	}

	b.mu.Lock()
	b.reqs = append(b.reqs, midTurnRequest{summary: summary, msgs: req.Messages})
	msg := map[string]any{"role": "assistant"}
	finish := "stop"
	switch {
	case summary:
		b.summaries++
		msg["content"] = fmt.Sprintf("SUMMARY-%d", b.summaries)
	case b.turnReqs < b.steps:
		b.turnReqs++
		msg["content"] = nil
		msg["tool_calls"] = []any{map[string]any{
			"id": fmt.Sprintf("call_%d", b.turnReqs), "type": "function", "index": 0,
			"function": map[string]any{"name": "ask_user", "arguments": `{"question":"more?"}`},
		}}
		finish = "tool_calls"
	default:
		b.turnReqs++
		msg["content"] = "done"
	}
	b.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "fake", "object": "chat.completion", "model": "fake",
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
		// A small reported size keeps the end-of-turn trigger out of the way:
		// whatever compacts in these tests compacted mid-turn.
		"usage": map[string]any{"prompt_tokens": promptTokens, "completion_tokens": 1, "total_tokens": promptTokens + 1},
	})
}

func (b *midTurnBackend) summaryCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.summaries
}

func (b *midTurnBackend) all() []midTurnRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]midTurnRequest(nil), b.reqs...)
}

// turns returns only the turn's own requests, without the summaries.
func (b *midTurnBackend) turns() []midTurnRequest {
	var out []midTurnRequest
	for _, r := range b.all() {
		if !r.summary {
			out = append(out, r)
		}
	}
	return out
}

// midTurnEvents records the status and compaction callbacks in order.
type midTurnEvents struct {
	mu sync.Mutex
	ev []string
}

func (e *midTurnEvents) add(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ev = append(e.ev, s)
}

func (e *midTurnEvents) list() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.ev...)
}

// runMidTurn runs one turn in which ask_user answers with answers, in order,
// and returns the backend's view of it.
func runMidTurn(t *testing.T, answers []string) (*Session, *midTurnBackend, *midTurnEvents) {
	t.Helper()
	return runMidTurnReporting(t, answers, 0)
}

// runMidTurnReporting is runMidTurn against a backend that counts extra tokens
// beyond the messages of every turn request.
func runMidTurnReporting(t *testing.T, answers []string, extra int) (*Session, *midTurnBackend, *midTurnEvents) {
	t.Helper()
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	backend := &midTurnBackend{steps: len(answers), extra: extra}
	srv := httptest.NewServer(backend)
	t.Cleanup(srv.Close)

	events := &midTurnEvents{}
	var asked int
	var askMu sync.Mutex
	cfg := types.Config{
		Model:   "fake-model",
		APIKey:  "fake-key",
		BaseURL: srv.URL + "/v1",
		Prompt:  midTurnPrompt,
		// The default approval mode keeps ask_user in the tool set; auto
		// approval hides it.
		AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 1},
		Compaction: types.CompactionConfig{
			MaxContextTokens: midTurnWindow, ReserveTokens: midTurnReserve, Threshold: 0.8, KeepRecent: 2,
		},
	}
	s, err := NewSession(context.Background(), cfg, Callbacks{
		OnAskUser: func(AskRequest) string {
			askMu.Lock()
			defer askMu.Unlock()
			a := answers[asked]
			asked++
			return a
		},
		OnStatus:      func(st string) { events.add("status:" + st) },
		OnCompactDone: func(before, after int) { events.add(fmt.Sprintf("compacted:%d>%d", before, after)) },
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	done := make(chan error, 1)
	go func() {
		_, err := s.SendMessage("start")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the turn did not finish")
	}
	return s, backend, events
}

// answer is a tool result of about tokens tokens that starts with marker.
func answer(marker string, tokens int) string {
	return marker + " " + strings.Repeat("x", tokens*4)
}

// The real failure: a turn that starts well under the trigger grows past the
// window through its own tool results, because auto-compaction only ran after
// the turn. Two 7500-token results and the ~1300-token system prompt take the
// third request to about 16300 tokens, over the 15200 trigger; the turn must
// compact before sending it.
//
// The results are sized so the request lands between 80% and 90% of the
// 19000 budget. There progressivePrune leaves a one-line result as it is, so
// the request still crosses the trigger. Above 90% it elides the older result
// and the request drops under the trigger without a summary, which is the
// compression working, not the compaction under test.
func TestMidTurnCompactionKeepsEveryRequestUnderTheTrigger(t *testing.T) {
	_, backend, events := runMidTurn(t, []string{
		answer("FIRST_ANSWER", 7500), answer("SECOND_ANSWER", 7500), answer("THIRD_ANSWER", 1000),
	})

	turns := backend.turns()
	if len(turns) != 4 {
		t.Fatalf("backend saw %d turn requests, want 4 (three tool steps and the answer)", len(turns))
	}
	for i, r := range turns {
		if r.tokens() >= midTurnTrigger {
			t.Fatalf("turn request %d carried %d tokens, at or over the %d trigger", i+1, r.tokens(), midTurnTrigger)
		}
	}

	// The summary was asked for between the second and third turn request, so
	// it was the mid-turn path and not the end-of-turn one.
	var order []string
	for _, r := range backend.all() {
		if r.summary {
			order = append(order, "summary")
		} else {
			order = append(order, "turn")
		}
	}
	if got := strings.Join(order, ","); got != "turn,turn,summary,turn,turn" {
		t.Fatalf("request order = %s, want turn,turn,summary,turn,turn", got)
	}

	var compacted, status int
	for _, e := range events.list() {
		if strings.HasPrefix(e, "compacted:") {
			compacted++
		}
		if e == "status:Compacting conversation…" {
			status++
		}
	}
	if compacted != 1 || status != 1 {
		t.Fatalf("mid-turn compaction was announced %d times with %d status lines, want 1 and 1; events: %v", compacted, status, events.list())
	}
}

// Once compacted, the next step reuses the summary: the fourth request is under
// the trigger and must not pay for a second summary, and both requests after
// the compaction carry the same one.
func TestMidTurnCompactionReusesTheSummary(t *testing.T) {
	_, backend, _ := runMidTurn(t, []string{
		answer("FIRST_ANSWER", 7500), answer("SECOND_ANSWER", 7500), answer("THIRD_ANSWER", 1000),
	})

	if n := backend.summaryCount(); n != 1 {
		t.Fatalf("summarized %d times in the turn, want 1", n)
	}
	turns := backend.turns()
	for _, i := range []int{2, 3} {
		r := turns[i]
		if !r.contains("SUMMARY-1") {
			t.Fatalf("turn request %d does not carry the summary", i+1)
		}
		if r.contains("FIRST_ANSWER") {
			t.Fatalf("turn request %d still carries the summarized tool result", i+1)
		}
		if !r.contains("SECOND_ANSWER") {
			t.Fatalf("turn request %d lost the kept tail", i+1)
		}
	}
	if !turns[3].contains("THIRD_ANSWER") {
		t.Fatal("the last request lost the result of the step after compaction")
	}
}

// The requests after compaction must still be valid: the system prompt is there
// and every tool result follows the assistant message that called it.
func TestMidTurnCompactionKeepsSystemPromptAndToolPairs(t *testing.T) {
	_, backend, _ := runMidTurn(t, []string{
		answer("FIRST_ANSWER", 7500), answer("SECOND_ANSWER", 7500), answer("THIRD_ANSWER", 1000),
	})

	turns := backend.turns()
	if len(turns) != 4 || !turns[2].contains("SUMMARY-1") {
		t.Fatal("the turn did not compact, so the checks below would prove nothing")
	}
	for _, i := range []int{2, 3} {
		r := turns[i]
		if !r.contains(midTurnPrompt) {
			t.Fatalf("turn request %d lost the system prompt", i+1)
		}
		called := map[string]bool{}
		for j, m := range r.msgs {
			for _, tc := range m.ToolCalls {
				called[tc.ID] = true
			}
			if m.Role == "tool" && !called[m.ToolCallID] {
				t.Fatalf("turn request %d: tool result %d (%s) has no earlier call", i+1, j, m.ToolCallID)
			}
		}
		for id := range called {
			found := false
			for _, m := range r.msgs {
				if m.Role == "tool" && m.ToolCallID == id {
					found = true
				}
			}
			if !found {
				t.Fatalf("turn request %d: call %s has no result", i+1, id)
			}
		}
	}
}

// The next turn must not start from the raw history the turn compacted away:
// the session's fragment ends the turn in the compacted form, and the display
// copy records the compaction the way compactHistory does.
func TestMidTurnCompactionLeavesTheFragmentCompacted(t *testing.T) {
	s, _, _ := runMidTurn(t, []string{
		answer("FIRST_ANSWER", 7500), answer("SECOND_ANSWER", 7500), answer("THIRD_ANSWER", 1000),
	})

	s.historyMu.Lock()
	msgs := append([]openai.ChatCompletionMessage(nil), s.fragment.Messages...)
	display := append([]openai.ChatCompletionMessage(nil), s.messages...)
	s.historyMu.Unlock()

	has := func(sub string) bool {
		for _, m := range msgs {
			if strings.Contains(m.Content, sub) {
				return true
			}
		}
		return false
	}
	if has("FIRST_ANSWER") {
		t.Fatal("the fragment still holds the tool result the turn summarized")
	}
	if !has("SUMMARY-1") || !has("SECOND_ANSWER") || !has("THIRD_ANSWER") {
		t.Fatal("the fragment is missing the summary or the kept tail")
	}
	if !fragmentHasSystemContent(s.fragment, s.systemPrompt) {
		t.Fatal("the fragment lost the system prompt")
	}
	if last := msgs[len(msgs)-1]; last.Role != "assistant" || last.Content != "done" {
		t.Fatalf("the fragment does not end with the answer: %+v", last)
	}
	if len(display) == 0 || !strings.HasPrefix(display[0].Content, "Compacted ") {
		t.Fatalf("the display copy does not record the compaction: %+v", display)
	}
	if got := display[len(display)-1]; got.Role != "assistant" || got.Content != "done" {
		t.Fatalf("the display copy does not end with the answer: %+v", got)
	}
}

// Under the trigger nothing is summarized and the history stays whole.
func TestNoMidTurnCompactionUnderTheTrigger(t *testing.T) {
	s, backend, events := runMidTurn(t, []string{
		answer("FIRST_ANSWER", 1000), answer("SECOND_ANSWER", 1000), answer("THIRD_ANSWER", 1000),
	})

	if n := backend.summaryCount(); n != 0 {
		t.Fatalf("summarized %d times under the trigger, want 0", n)
	}
	for _, e := range events.list() {
		if strings.HasPrefix(e, "compacted:") {
			t.Fatalf("compaction was announced under the trigger: %v", events.list())
		}
	}
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	found := 0
	for _, m := range s.fragment.Messages {
		for _, marker := range []string{"FIRST_ANSWER", "SECOND_ANSWER", "THIRD_ANSWER"} {
			if strings.Contains(m.Content, marker) {
				found++
			}
		}
	}
	if found != 3 {
		t.Fatalf("the fragment holds %d of the 3 tool results, want all 3", found)
	}
}

// A second compaction in the same turn summarizes the first summary with what
// came after it, and must map back onto the fragment at the right message:
// off by the length of the first replacement, it would drop or repeat a step.
func TestMidTurnCompactionTwiceInOneTurn(t *testing.T) {
	s, backend, _ := runMidTurn(t, []string{
		answer("FIRST_ANSWER", 7500), answer("SECOND_ANSWER", 7500),
		answer("THIRD_ANSWER", 7500), answer("FOURTH_ANSWER", 1000),
	})

	if n := backend.summaryCount(); n != 2 {
		t.Fatalf("summarized %d times, want 2", n)
	}
	var summaries []midTurnRequest
	for _, r := range backend.all() {
		if r.summary {
			summaries = append(summaries, r)
		}
	}
	if !summaries[1].contains("SUMMARY-1") || summaries[1].contains("FIRST_ANSWER") {
		t.Fatal("the second summary did not build on the first one")
	}
	for i, r := range backend.turns() {
		if r.tokens() >= midTurnTrigger {
			t.Fatalf("turn request %d carried %d tokens, at or over the %d trigger", i+1, r.tokens(), midTurnTrigger)
		}
	}

	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	var roles []string
	system := 0
	for _, m := range s.fragment.Messages {
		roles = append(roles, m.Role)
		if m.Role == "system" && m.Content == s.systemPrompt {
			system++
		}
		for _, gone := range []string{"FIRST_ANSWER", "SECOND_ANSWER", "SUMMARY-1"} {
			if strings.Contains(m.Content, gone) {
				t.Fatalf("the fragment still holds %s", gone)
			}
		}
	}
	if system != 1 {
		t.Fatalf("the fragment holds the system prompt %d times, want 1", system)
	}
	// system, summary, then the third and fourth steps whole, then the answer.
	want := "system,user,assistant,tool,assistant,tool,assistant"
	if got := strings.Join(roles, ","); got != want {
		t.Fatalf("fragment roles = %s, want %s", got, want)
	}
}

// The messages are not the whole prompt: tool schemas and tokenizer skew add to
// it, and the backend's figure counts them. Here the messages alone stay under
// the trigger (about 10800 tokens by the third request), but the backend counts
// 6000 more, so the real prompt is over it and the turn must compact.
func TestMidTurnCompactionCountsWhatTheBackendReports(t *testing.T) {
	_, backend, _ := runMidTurnReporting(t, []string{
		answer("FIRST_ANSWER", 5000), answer("SECOND_ANSWER", 5000), answer("THIRD_ANSWER", 100),
	}, 6000)

	var order []string
	for _, r := range backend.all() {
		if r.summary {
			order = append(order, "summary")
		} else {
			order = append(order, "turn")
		}
	}
	if got := strings.Join(order, ","); !strings.HasPrefix(got, "turn,turn,summary,turn") {
		t.Fatalf("request order = %s, want a summary before the third turn request", got)
	}
}
