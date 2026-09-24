package chat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/types"
	openai "github.com/sashabaranov/go-openai"
)

// rateLimitLLM fails the first N completion calls with a 429 rate-limit error,
// then succeeds. The error mirrors the real LocalAI client's format:
//
//	localai stream: status 429: {"error":{"message":"Rate limit exceeded for user: ... Limit resets at: 2026-09-21 15:48:40 UTC","type":"None","param":"None","code":"429"}}
type rateLimitLLM struct {
	mu       sync.Mutex
	failures int   // remaining calls that should fail
	err      error // failure to return; nil means the 429 below
	calls    int
	asks     int
}

func (r *rateLimitLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	if err := ctx.Err(); err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}
	r.mu.Lock()
	r.calls++
	fail := r.failures > 0
	if fail {
		r.failures--
	}
	r.mu.Unlock()

	if fail && r.err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, r.err
	}
	if fail {
		return cogito.LLMReply{}, cogito.LLMUsage{},
			errors.New(`localai stream: status 429: {"error":{"message":"Rate limit exceeded for user: test@example.com. This may be due to configured key limits or free trial restrictions.Consider upgrading to a paid plan to remove limits. Limit type: tokens. Limit resets at: 2026-09-21 15:48:40 UTC","type":"None","param":"None","code":"429"}}`)
	}
	return cogito.LLMReply{
		ChatCompletionResponse: openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{{
				Message:      openai.ChatCompletionMessage{Role: "assistant", Content: "ok"},
				FinishReason: openai.FinishReasonStop,
			}},
		},
	}, cogito.LLMUsage{PromptTokens: 10, CompletionTokens: 1, TotalTokens: 11}, nil
}

func (r *rateLimitLLM) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	if err := ctx.Err(); err != nil {
		return f, err
	}
	r.mu.Lock()
	r.asks++
	r.mu.Unlock()
	return f.AddMessage("assistant", "summary of earlier turns"), nil
}

func newRateLimitSession(t *testing.T, llm cogito.LLM) *Session {
	t.Helper()
	s := &Session{
		ctx:          context.Background(),
		llm:          llm,
		llmModel:     "qwen",
		systemPrompt: "you are the rate-limit-test assistant",
		// MaxRetries=1 so one completion call is one ExecuteTools run,
		// same reasoning as newOverflowSession.
		cogitoOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 1},
		agentManager:  cogito.NewAgentManager(),
		agentLogs:     newAgentLogStore(),
		inject:        make(chan openai.ChatCompletionMessage, 8),
		compaction:    types.CompactionConfig{MaxContextTokens: 262144, Threshold: 0.8, KeepRecent: 2, ReserveTokens: 4096},
	}
	s.fragment = cogito.NewFragment(
		openai.ChatCompletionMessage{Role: "user", Content: "u1"},
		openai.ChatCompletionMessage{Role: "assistant", Content: "a1"},
	)
	return s
}

// stubRetrySleep replaces the retry sleep with one that returns at once and
// records each requested wait.
func stubRetrySleep(t *testing.T) *[]time.Duration {
	t.Helper()
	var mu sync.Mutex
	waits := &[]time.Duration{}
	prev := retrySleep
	retrySleep = func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		*waits = append(*waits, d)
		mu.Unlock()
		return ctx.Err()
	}
	t.Cleanup(func() { retrySleep = prev })
	return waits
}

func resetAt(d time.Duration) string {
	return "Limit resets at: " + time.Now().UTC().Add(d).Format("2006-01-02 15:04:05") + " UTC"
}

// --- classifyBackendError ---

func TestClassifyBackendError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want backendErrorClass
	}{
		{"nil", nil, errFatal},
		{"localai 429 as reported", errors.New(`failed to select tool: failed to pick tool: tool selection failed: failed to make a streaming decision after 3 attempts: localai stream: status 429: {"error":{"message":"Rate limit exceeded for user: x. Limit type: tokens. Limit resets at: 2026-09-21 21:40:55 UTC","type":"None","param":"None","code":"429"}}`), errRateLimited},
		{"openai 429", errors.New("error, status code: 429, status: 429 Too Many Requests, message: slow down"), errRateLimited},
		{"anthropic rate_limit_error", errors.New(`{"type":"error","error":{"type":"rate_limit_error","message":"x"}}`), errRateLimited},
		{"too many requests", errors.New("too many requests"), errRateLimited},
		{"localai 503", errors.New("localai stream: status 503: upstream busy"), errTransient},
		{"openai 502", errors.New("error, status code: 502, status: 502 Bad Gateway"), errTransient},
		{"anthropic 529 overloaded", errors.New(`{"type":"overloaded_error","message":"Overloaded"}`), errTransient},
		{"request timeout 408", errors.New("localai stream: status 408: timeout"), errTransient},
		{"connection refused", errors.New("dial tcp 127.0.0.1:8080: connect: connection refused"), errTransient},
		{"connection reset", errors.New("read tcp: connection reset by peer"), errTransient},
		{"bare eof", errors.New(`Post "http://x/v1/chat/completions": EOF`), errTransient},
		{"client timeout", errors.New("net/http: request canceled (Client.Timeout exceeded while awaiting headers)"), errTransient},
		{"grpc goaway", errors.New(`failed to select tool: failed to pick tool: tool selection failed: failed to make a streaming decision after 3 attempts: localai stream: rpc error: code = Unavailable desc = closing transport due to: connection error: desc = "error reading from server: EOF", received prior goaway: code: NO_ERROR`), errTransient},
		{"grpc unavailable", errors.New("rpc error: code = Unavailable desc = transport is closing"), errTransient},
		{"unknown error is transient by default", errors.New("something we have never seen before"), errTransient},
		{"bad request", errors.New("localai stream: status 400: invalid tool schema"), errFatal},
		{"unauthorized", errors.New("error, status code: 401, status: 401 Unauthorized"), errFatal},
		{"model not found", errors.New("localai stream: status 404: model not found"), errFatal},
		{"overflow reported as 500", errors.New("localai stream: status 500: request (368203 tokens) exceeds the available context size (262144 tokens)"), errFatal},
		{"tool not found", errors.New("tool frobnicate not found"), errFatal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyBackendError(tc.err); got != tc.want {
				t.Fatalf("classifyBackendError(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

func TestCanRetryTurn(t *testing.T) {
	rl := errors.New("localai stream: status 429: Rate limit exceeded")
	live := context.Background()
	if !canRetryTurn(live, rl) {
		t.Fatal("a live rate-limited turn must be retryable")
	}
	if !canRetryTurn(live, errors.New("localai stream: status 503: busy")) {
		t.Fatal("a live turn with a transient error must be retryable")
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if canRetryTurn(cancelled, rl) {
		t.Fatal("an interrupted turn was retryable; Ctrl+C would be answered with a re-send")
	}
	if canRetryTurn(live, errors.New("localai stream: status 401: bad key")) {
		t.Fatal("a fatal error was retryable")
	}
}

// --- waits ---

func TestRateLimitResetWaitParsesFutureReset(t *testing.T) {
	wait, ok := rateLimitResetWait(errors.New("status 429: " + resetAt(90*time.Second)))
	if !ok || wait < 85*time.Second || wait > 90*time.Second {
		t.Fatalf("rateLimitResetWait = %s, %v; want about 90s", wait, ok)
	}
	if _, ok := rateLimitResetWait(errors.New("status 429: " + resetAt(-time.Minute))); ok {
		t.Fatal("a reset in the past must be ignored")
	}
}

func TestRequestWaitBacksOffWithoutReset(t *testing.T) {
	err := errors.New("localai stream: status 503: busy")
	for attempt, want := range []time.Duration{2 * time.Second, 4 * time.Second} {
		if got, ok := requestWait(err, attempt); !ok || got != want {
			t.Fatalf("requestWait(attempt %d) = %s, %v; want %s", attempt, got, ok, want)
		}
	}
}

func TestRequestWaitLeavesLongResetToSession(t *testing.T) {
	if _, ok := requestWait(errors.New("status 429: "+resetAt(10*time.Minute)), 0); ok {
		t.Fatal("the request layer must not sleep through a reset minutes away")
	}
	if got, ok := requestWait(errors.New("status 429: "+resetAt(10*time.Second)), 0); !ok || got < 10*time.Second || got > 12*time.Second {
		t.Fatalf("requestWait = %s, %v; want to wait just past a close reset", got, ok)
	}
}

func TestRequestWaitRefusesFatal(t *testing.T) {
	if _, ok := requestWait(errors.New("status 400: bad"), 0); ok {
		t.Fatal("a fatal error must not be retried at the request layer")
	}
}

func TestTurnWaitWaitsForReset(t *testing.T) {
	got := turnWait(errors.New("status 429: "+resetAt(4*time.Minute)), 0)
	if got < 4*time.Minute-5*time.Second || got > 4*time.Minute+2*time.Second {
		t.Fatalf("turnWait = %s; want the time until the reset", got)
	}
	if got := turnWait(errors.New("status 429: "+resetAt(2*time.Hour)), 0); got != turnMaxWait {
		t.Fatalf("turnWait = %s; want it capped at %s", got, turnMaxWait)
	}
}

func TestTurnWaitBacksOffWithJitter(t *testing.T) {
	err := errors.New("status 503: busy")
	for retry, base := range []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second} {
		got := turnWait(err, retry)
		if got < base || got > base+base/5 {
			t.Fatalf("turnWait(retry %d) = %s; want %s plus up to 20%%", retry, got, base)
		}
	}
	if got := turnWait(err, 40); got < turnBackoffCap || got > turnBackoffCap+turnBackoffCap/5 {
		t.Fatalf("turnWait(retry 40) = %s; want it capped near %s", got, turnBackoffCap)
	}
}

// --- retryRequest (the trackedLLM layer) ---

func TestRetryRequestRetriesRecoverableErrors(t *testing.T) {
	waits := stubRetrySleep(t)
	calls := 0
	result, err := retryRequest(context.Background(), func() (string, error) {
		calls++
		if calls < 3 {
			return "", errors.New("localai stream: status 429: rate limit exceeded")
		}
		return "ok", nil
	})
	if err != nil || result != "ok" {
		t.Fatalf("retryRequest = %q, %v; want ok", result, err)
	}
	if calls != 3 || len(*waits) != 2 {
		t.Fatalf("calls = %d, sleeps = %d; want 3 calls and 2 sleeps", calls, len(*waits))
	}
}

func TestRetryRequestDoesNotSleepAfterLastAttempt(t *testing.T) {
	waits := stubRetrySleep(t)
	calls := 0
	_, err := retryRequest(context.Background(), func() (string, error) {
		calls++
		return "", errors.New("localai stream: status 503: busy")
	})
	if err == nil {
		t.Fatal("expected the error once attempts ran out")
	}
	if calls != requestAttempts || len(*waits) != requestAttempts-1 {
		t.Fatalf("calls = %d, sleeps = %d; want %d calls and %d sleeps", calls, len(*waits), requestAttempts, requestAttempts-1)
	}
}

func TestRetryRequestReturnsFatalAtOnce(t *testing.T) {
	stubRetrySleep(t)
	calls := 0
	_, err := retryRequest(context.Background(), func() (string, error) {
		calls++
		return "", errors.New("localai stream: status 401: bad key")
	})
	if err == nil || calls != 1 {
		t.Fatalf("calls = %d, err = %v; a fatal error must not be retried", calls, err)
	}
}

func TestRetryRequestHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	if _, err := retryRequest(ctx, func() (string, error) {
		calls++
		return "", errors.New("status 429")
	}); err == nil || calls != 0 {
		t.Fatalf("calls = %d, err = %v; a cancelled context must stop before the first call", calls, err)
	}
}

// --- resumableFragment ---

func TestResumableFragmentKeepsProgress(t *testing.T) {
	started := cogito.NewFragment(openai.ChatCompletionMessage{Role: "user", Content: "do it"})
	failed := started.AddMessage("assistant", "").
		AddMessage("tool", "result")
	failed.Messages[1].ToolCalls = []openai.ToolCall{{ID: "1", Function: openai.FunctionCall{Name: "bash"}}}

	got, progressed := resumableFragment(started, failed)
	if !progressed || len(got.Messages) != 3 {
		t.Fatalf("progressed = %v, messages = %d; want the failed attempt's tool result kept", progressed, len(got.Messages))
	}
}

func TestResumableFragmentRejectsDanglingToolCall(t *testing.T) {
	started := cogito.NewFragment(openai.ChatCompletionMessage{Role: "user", Content: "do it"})
	failed := started.AddMessage("assistant", "")
	failed.Messages[1].ToolCalls = []openai.ToolCall{{ID: "1", Function: openai.FunctionCall{Name: "bash"}}}

	got, progressed := resumableFragment(started, failed)
	if progressed || len(got.Messages) != 1 {
		t.Fatalf("progressed = %v, messages = %d; a tool call without its result cannot be resent", progressed, len(got.Messages))
	}
}

func TestResumableFragmentWithoutProgress(t *testing.T) {
	started := cogito.NewFragment(openai.ChatCompletionMessage{Role: "user", Content: "do it"})
	if got, progressed := resumableFragment(started, cogito.Fragment{}); progressed || len(got.Messages) != 1 {
		t.Fatalf("progressed = %v, messages = %d; want the attempt's starting point", progressed, len(got.Messages))
	}
}

// --- messages ---

func TestRateLimitMessage(t *testing.T) {
	if msg := rateLimitMessage(errors.New("status 429: " + resetAt(5*time.Minute))); !strings.Contains(msg, "resets in") {
		t.Fatalf("message should include the reset: %q", msg)
	}
	if msg := rateLimitMessage(errors.New("status 429: too many requests")); strings.Contains(msg, "resets in") {
		t.Fatalf("message should not invent a reset: %q", msg)
	}
}

func TestHumanizeErrorRateLimit(t *testing.T) {
	err := errors.New(`localai stream: status 429: {"error":{"message":"Rate limit exceeded"}}`)
	fe, ok := humanizeError(err).(*FriendlyError)
	if !ok {
		t.Fatalf("humanizeError returned %T, want *FriendlyError", humanizeError(err))
	}
	if !strings.Contains(fe.msg, "rate-limited") || !errors.Is(fe, err) {
		t.Fatalf("friendly error = %q; want a rate-limit message wrapping the original", fe.msg)
	}
}

// --- session-level recovery ---

// A short rate limit is absorbed by the request layer: the turn never fails.
func TestRateLimitRecoveredByRequestLayer(t *testing.T) {
	stubRetrySleep(t)
	s := newRateLimitSession(t, &rateLimitLLM{failures: requestAttempts - 1})

	if got, err := s.SendMessage("what changed?"); err != nil || got == "" {
		t.Fatalf("SendMessage = %q, %v; the request layer should have retried", got, err)
	}
	if s.turnRetries() != 0 {
		t.Fatalf("turn retries = %d; the session layer should not have fired", s.turnRetries())
	}
}

// A rate limit that outlasts the request layer is waited out by the session
// layer, and the turn succeeds instead of stopping.
func TestRateLimitOutlastingRequestLayerStillCompletes(t *testing.T) {
	waits := stubRetrySleep(t)
	s := newRateLimitSession(t, &rateLimitLLM{failures: 4 * requestAttempts})

	got, err := s.SendMessage("what changed?")
	if err != nil || got == "" {
		t.Fatalf("SendMessage = %q, %v; want the turn to survive the rate limit", got, err)
	}
	if s.turnRetries() != 4 {
		t.Fatalf("turn retries = %d, want 4", s.turnRetries())
	}
	if len(*waits) == 0 {
		t.Fatal("no waits were requested")
	}
}

func TestTransientErrorRetriesTurn(t *testing.T) {
	stubRetrySleep(t)
	llm := &rateLimitLLM{failures: 2 * requestAttempts, err: errors.New("localai stream: status 502: bad gateway")}
	s := newRateLimitSession(t, llm)

	if _, err := s.SendMessage("what changed?"); err != nil {
		t.Fatalf("SendMessage failed on a transient error: %v", err)
	}
	if s.turnRetries() != 2 {
		t.Fatalf("turn retries = %d, want 2", s.turnRetries())
	}
}

func TestFatalErrorIsNotRetried(t *testing.T) {
	stubRetrySleep(t)
	llm := &rateLimitLLM{failures: 99, err: errors.New("localai stream: status 401: invalid api key")}
	s := newRateLimitSession(t, llm)

	if _, err := s.SendMessage("what changed?"); err == nil {
		t.Fatal("expected the fatal error")
	}
	if s.turnRetries() != 0 || llm.calls != 1 {
		t.Fatalf("turn retries = %d, calls = %d; a fatal error must stop at once", s.turnRetries(), llm.calls)
	}
}

// A limit that never clears stops after the retry budget, with a friendly
// message. The user's message stays in history, as the transcript still shows
// it, followed by a note that the request failed.
func TestRateLimitStopsAfterBudget(t *testing.T) {
	stubRetrySleep(t)
	s := newRateLimitSession(t, &rateLimitLLM{failures: 1 << 20})

	_, err := s.SendMessage("what changed?")
	if err == nil {
		t.Fatal("expected the turn to fail when the rate limit never clears")
	}
	if s.turnRetries() != turnRetryBudget {
		t.Fatalf("turn retries = %d, want %d", s.turnRetries(), turnRetryBudget)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "rate-limited") {
		t.Fatalf("error = %v; want a friendly rate-limit message", err)
	}
	msgs := s.fragment.Messages
	if n := len(msgs); n < 2 || msgs[n-2].Content != "what changed?" || !strings.HasPrefix(msgs[n-1].Content, "[This request failed") {
		t.Fatalf("history after a failed turn = %+v; want the user message then the failure note", msgs)
	}
}

type interruptingRateLimitLLM struct {
	rateLimitLLM
	s *Session
}

func (i *interruptingRateLimitLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	i.s.Interrupt()
	return i.rateLimitLLM.CreateChatCompletion(ctx, req)
}

func TestRateLimitDoesNotRetryAnInterruptedTurn(t *testing.T) {
	stubRetrySleep(t)
	llm := &interruptingRateLimitLLM{rateLimitLLM: rateLimitLLM{failures: 99}}
	s := newRateLimitSession(t, llm)
	llm.s = s

	if _, err := s.SendMessage("what changed?"); err == nil {
		t.Fatal("expected the interrupted turn to fail")
	}
	if s.turnRetries() != 0 {
		t.Fatalf("turn retries = %d on an interrupted turn, want 0", s.turnRetries())
	}
}

func TestTurnRetryIsAnnounced(t *testing.T) {
	stubRetrySleep(t)
	rec := &retryStatusRecorder{}
	s := newRateLimitSession(t, &rateLimitLLM{failures: 2 * requestAttempts})
	s.callbacks = rec.callbacks()

	_, _ = s.SendMessage("what changed?")

	rec.mu.Lock()
	lines := append([]string(nil), rec.lines...)
	rec.mu.Unlock()
	for _, l := range lines {
		if strings.Contains(l, "Rate limited — retrying in") {
			return
		}
	}
	t.Fatalf("a rate-limit retry went unannounced; statuses seen: %v", lines)
}

// The countdown goes down while the turn waits, instead of showing the first
// wait for the whole of it.
func TestWaitTurnRetryCountsDown(t *testing.T) {
	stubRetrySleep(t)
	var lines []string
	err := waitTurnRetry(context.Background(), errors.New("status 429"), 3*time.Second, 0,
		func(s string) { lines = append(lines, s) })
	if err != nil {
		t.Fatalf("waitTurnRetry: %v", err)
	}
	want := []string{
		"Rate limited — retrying in 3s (1/10, ctrl+c to stop)…",
		"Rate limited — retrying in 2s (1/10, ctrl+c to stop)…",
		"Rate limited — retrying in 1s (1/10, ctrl+c to stop)…",
		retryResumeStatus,
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("statuses = %q, want %q", lines, want)
	}
}

// A retry that then succeeds must not leave the rate-limit line on screen: a
// plain streamed answer emits no status of its own to replace it.
func TestTurnRetryStatusIsClearedAfterTheWait(t *testing.T) {
	stubRetrySleep(t)
	rec := &retryStatusRecorder{}
	s := newRateLimitSession(t, &rateLimitLLM{failures: 2 * requestAttempts})
	s.callbacks = rec.callbacks()

	if _, err := s.SendMessage("what changed?"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.lines) == 0 || rec.lines[len(rec.lines)-1] != retryResumeStatus {
		t.Fatalf("last status = %q, want %q; statuses seen: %v", rec.lines[len(rec.lines)-1], retryResumeStatus, rec.lines)
	}
}

func TestMalformedResponsesDoNotRetryWholeTurn(t *testing.T) {
	for _, message := range []string{
		"failed to select tool: openai-responses: completed without an assistant message (id=resp_1, status=completed, output_types=[])",
		"openai-responses: incomplete response (id=resp_2, reason=max_output_tokens)",
	} {
		if got := classifyBackendError(errors.New(message)); got != errFatal {
			t.Fatalf("malformed response classified as %v", got)
		}
	}
}
