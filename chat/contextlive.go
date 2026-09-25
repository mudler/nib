package chat

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"github.com/mudler/cogito"
	"github.com/mudler/xlog"
	"github.com/sashabaranov/go-openai"
)

// liveUsage records the prompt-token count reported by the most recent request
// of the turn in flight, so ContextTokens can answer "how full is the context
// RIGHT NOW" instead of "how full was it when the last turn ended".
//
// It exists because the session cannot read the running turn's own figure.
// SendMessage hands cogito a deep copy of Fragment.Status (so a failed run
// cannot pollute the session's PastActions/ToolsCalled — see the copy in
// SendMessage) and reassigns s.fragment only once ExecuteTools has returned.
// cogito does update LastUsage on that copy after every call in the tool loop,
// but reading it from the UI goroutine while the turn goroutine writes it is a
// data race, and cogito offers no per-call usage callback to subscribe to.
//
// So the figure is taken one layer lower, from the LLM client itself: every
// request a turn makes passes through the wrapper below, on the turn
// goroutine, and stores its prompt tokens here. A single int64 is enough —
// prompt tokens are the whole conversation as the backend counted it, not a
// delta to accumulate.
//
// active distinguishes "no turn is running" from "a turn is running and has
// not been billed yet". Without it a zero would be indistinguishable from an
// idle session and ContextTokens could not tell which source to trust.
type liveUsage struct {
	prompt atomic.Int64
	active atomic.Bool
}

// begin arms the tracker for a new turn, discarding the previous turn's
// figure: the fragment is the authority between turns.
func (l *liveUsage) begin() {
	l.prompt.Store(0)
	l.active.Store(true)
}

// end disarms it, handing authority back to s.fragment — which by then holds
// the completed run's Status, and which compaction may since have shrunk.
func (l *liveUsage) end() {
	l.active.Store(false)
	l.prompt.Store(0)
}

// reset drops the in-flight figure without disarming: compaction replaced the
// conversation the last request was measured against, so that number now
// describes history nobody is sending any more. ContextTokens falls back to
// the rebuilt fragment's estimate until the next request reports a real one.
func (l *liveUsage) reset() {
	l.prompt.Store(0)
}

// record stores a request's prompt tokens. Zero and negative counts are
// ignored: a backend that reports no usage must not erase the last real
// figure and drop the gauge back to the estimate mid-turn.
func (l *liveUsage) record(n int) {
	if n > 0 {
		l.prompt.Store(int64(n))
	}
}

// promptTokens returns the in-flight figure, or 0 when no turn is running or
// none of its requests has reported usage yet.
func (l *liveUsage) promptTokens() int {
	if !l.active.Load() {
		return 0
	}
	return int(l.prompt.Load())
}

// trackedLLM is the LLM the turn loop actually calls: the session's client,
// with every request's reported usage recorded on the way back.
//
// It also sizes each request's output reservation (see clampOutputTokens).
// limits reports the session's output cap and context window; nil leaves every
// request as the caller built it.
type trackedLLM struct {
	cogito.LLM
	live   *liveUsage
	limits func() (cap, window int)
}

// clamp applies clampOutputTokens with the session's current limits.
func (t *trackedLLM) clamp(request openai.ChatCompletionRequest) openai.ChatCompletionRequest {
	if t.limits == nil {
		return request
	}
	cap, window := t.limits()
	return clampOutputTokens(request, cap, window)
}

func (t *trackedLLM) CreateChatCompletion(ctx context.Context, request openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	request = t.clamp(request)
	var usage cogito.LLMUsage
	reply, err := retryRequest(ctx, func() (cogito.LLMReply, error) {
		r, u, e := t.LLM.CreateChatCompletion(ctx, request)
		usage = u
		return r, e
	})
	t.live.record(usage.PromptTokens)
	return reply, usage, err
}

func (t *trackedLLM) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	out, err := retryRequest(ctx, func() (cogito.Fragment, error) {
		return t.LLM.Ask(ctx, f)
	})
	if out.Status != nil {
		t.live.record(out.Status.LastUsage.PromptTokens)
	}
	return out, err
}

// trackedStreamingLLM is trackedLLM for a client that streams. The extra type
// is not optional: cogito picks the streaming path with a type assertion
// (`llm.(StreamingLLM)`), so a wrapper that did not also implement
// CreateChatCompletionStream would silently switch every streaming provider
// back to blocking requests — the thinking box and the reply would stop
// filling progressively.
type trackedStreamingLLM struct {
	trackedLLM
	stream cogito.StreamingLLM
}

// CreateChatCompletionStream relays the provider's events unchanged and reads
// the usage off the terminal "done" event, which is the only place a streamed
// request reports it.
//
// The relay goroutine is bounded by the source channel: it ends when the
// provider closes it, which cogito's own consumer loop already depends on.
func (t *trackedStreamingLLM) CreateChatCompletionStream(ctx context.Context, request openai.ChatCompletionRequest) (<-chan cogito.StreamEvent, error) {
	request = t.clamp(request)
	src, err := retryRequest(ctx, func() (<-chan cogito.StreamEvent, error) {
		return t.stream.CreateChatCompletionStream(ctx, request)
	})
	if err != nil {
		return src, err
	}
	out := make(chan cogito.StreamEvent)
	go func() {
		defer close(out)
		for ev := range src {
			if ev.Type == cogito.StreamEventDone {
				t.live.record(ev.Usage.PromptTokens)
			}
			out <- ev
		}
	}()
	return out, nil
}

// trackUsage wraps llm so the turn's requests report their size to live and
// reserve only the output room the window has left (limits may be nil),
// preserving streaming support when the client has it.
func trackUsage(llm cogito.LLM, live *liveUsage, limits func() (cap, window int)) cogito.LLM {
	t := trackedLLM{LLM: llm, live: live, limits: limits}
	if s, ok := llm.(cogito.StreamingLLM); ok {
		return &trackedStreamingLLM{trackedLLM: t, stream: s}
	}
	return &t
}

const (
	// minOutputTokens is the smallest output reservation a clamped request
	// carries. A prompt that already fills the window still gets this much,
	// and the overflow path deals with the rejection.
	minOutputTokens = 1024
	// outputSafetyMargin absorbs the error of the bytes/4 prompt estimate.
	outputSafetyMargin = 512
)

// clampOutputTokens sizes a request's output reservation to what the window
// has left after its prompt.
//
// The client sends its output cap as max_tokens on every request. A backend
// such as vLLM rejects a request when prompt + max_tokens exceeds the window,
// whatever the model actually generates. With a cap that is a large fraction
// of the window (regolo: 96000 of 210000), every prompt above window − cap
// failed, compaction summaries included. cogito applies the client's cap only
// when the request carries none, so setting it here decides the reservation.
//
// The result is min(cap, window − prompt − outputSafetyMargin), floored at
// minOutputTokens (and never above cap). The prompt estimate is bytes/4 over
// the messages and the JSON of the tool schemas, which are real prompt tokens.
// A request with an explicit value, or unknown limits (cap or window 0), is
// returned unchanged.
func clampOutputTokens(request openai.ChatCompletionRequest, cap, window int) openai.ChatCompletionRequest {
	if cap <= 0 || window <= 0 || request.MaxTokens != 0 || request.MaxCompletionTokens != 0 {
		return request
	}
	prompt := estimateTokens(request.Messages)
	if len(request.Tools) > 0 {
		if b, err := json.Marshal(request.Tools); err == nil {
			prompt += len(b) / 4
		}
	}
	v := min(cap, window-prompt-outputSafetyMargin)
	if v < minOutputTokens {
		v = min(minOutputTokens, cap)
	}
	if v < cap {
		xlog.Debug("output reservation clamped", "from", cap, "to", v, "prompt_estimate", prompt, "window", window)
	}
	request.MaxTokens = v
	return request
}
