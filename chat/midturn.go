package chat

import (
	"context"

	"github.com/mudler/cogito"
	"github.com/mudler/xlog"
	openai "github.com/sashabaranov/go-openai"
)

// turnCompactor compacts the conversation between the tool steps of a turn.
//
// Auto-compaction otherwise runs only after a turn, and one turn can call many
// tools: a turn that starts under the trigger can grow past the window through
// its own tool results, and the backend then rejects it. The overflow recovery
// catches that, but only by sending the whole turn again.
//
// cogito gives no hook between steps other than the messages manipulator, and
// the manipulator only rewrites the request: the fragment cogito carries
// through the loop, and returns at the end, keeps every raw message. So the
// compactor works in two halves. manipulate replaces the older part of each
// request with a summary, and remembers which leading messages of the fragment
// that summary stands for. apply then rewrites the fragment the run returns the
// same way, so the next turn starts from the compacted history.
//
// The mapping from request to fragment holds because nib's manipulator input is
// the fragment's own messages: nib sets no guidelines and no MCP prompts, which
// are what cogito would put in front of them (see forgetAbsentIDs for the same
// assumption). The boundary message is checked before a summary is reused, so a
// mismatch sends the raw history rather than a wrong one.
//
// It is used only from inside ExecuteTools, which calls the manipulator
// synchronously, and from SendMessage between runs, so it needs no lock.
type turnCompactor struct {
	s   *Session
	ctx context.Context

	// covered is how many leading fragment messages the replacement stands
	// for, and last is the final one of them.
	covered     int
	last        openai.ChatCompletionMessage
	replacement []openai.ChatCompletionMessage

	// sent is the byte/4 size of the messages of the last request.
	sent int
	// failed stops further attempts in this run after a summary failed: each
	// attempt is a whole LLM call, and the overflow recovery still stands
	// behind the turn.
	failed bool
}

func (s *Session) newTurnCompactor(ctx context.Context) *turnCompactor {
	return &turnCompactor{s: s, ctx: ctx}
}

// reset forgets the previous run's summary. The run that follows starts from
// s.fragment, which apply already rewrote. sent is kept: it still describes the
// last request, which is what the next usage report is about.
func (c *turnCompactor) reset() {
	c.covered, c.last, c.replacement, c.failed = 0, openai.ChatCompletionMessage{}, nil, false
}

// matches reports whether msgs starts with the history the summary stands for.
func (c *turnCompactor) matches(msgs []openai.ChatCompletionMessage) bool {
	return c.covered > 0 && len(msgs) >= c.covered && sameMessage(msgs[c.covered-1], c.last)
}

// sameMessage compares what identifies a message in a turn. A tool result or
// call is told apart by its ids even when two bodies are equal.
func sameMessage(a, b openai.ChatCompletionMessage) bool {
	if a.Role != b.Role || a.Content != b.Content || a.ToolCallID != b.ToolCallID || len(a.ToolCalls) != len(b.ToolCalls) {
		return false
	}
	for i := range a.ToolCalls {
		if a.ToolCalls[i].ID != b.ToolCalls[i].ID {
			return false
		}
	}
	return true
}

// manipulate is the cogito.WithMessagesManipulator body for a turn: it puts the
// summary in place of the history it covers, prunes and progressively
// compresses tool output (progressivePrune), and compacts when the result
// crosses the auto-compaction trigger.
func (c *turnCompactor) manipulate(msgs []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	base, repl := 0, 0
	view := msgs
	if c.matches(msgs) {
		base, repl = c.covered, len(c.replacement)
		view = make([]openai.ChatCompletionMessage, 0, repl+len(msgs)-base)
		view = append(append(view, c.replacement...), msgs[base:]...)
	}
	out := c.s.progressivePrune(view)

	overhead := c.overhead()
	if !c.failed && c.s.shouldCompactNow(estimateTokens(out)+overhead) {
		out = c.compact(msgs, out, base, repl, overhead)
	}
	c.sent = estimateTokens(out)
	return out
}

// overhead is how many more tokens the backend counted for the last request
// than the byte/4 estimate of its messages.
//
// The trigger has to be measured on the request about to go out, and only an
// estimate exists for that: the last reported usage lags by exactly the tool
// results that just arrived, which is the growth this is here to catch. The
// estimate covers the pruned messages, the same view pruning pressure is
// measured on, but misses the tool schemas and any tokenizer skew. The last
// report measured both, so its excess over the last estimate is added back.
func (c *turnCompactor) overhead() int {
	reported := c.s.live.promptTokens()
	if c.sent <= 0 || reported <= c.sent {
		return 0
	}
	return reported - c.sent
}

// compact summarizes the head of out and returns the request with the summary
// in its place, or out unchanged when there is nothing new to summarize or the
// summary failed. msgs is the fragment's view of the same request; base and
// repl say how out's leading replacement maps onto it.
func (c *turnCompactor) compact(msgs, out []openai.ChatCompletionMessage, base, repl, overhead int) []openai.ChatCompletionMessage {
	// The kept tail has to fit too, or the summary buys nothing and the next
	// step compacts again. Shrink it toward the last tool step when it does
	// not; splitForCompaction still never separates a call from its results.
	keep := 0
	for k := max(c.s.compactionConfig().KeepRecent, 1); k >= 1; k-- {
		h, t := splitForCompaction(out, k)
		if h == nil {
			continue
		}
		keep = k
		if !c.s.shouldCompactNow(estimateTokens(t) + overhead) {
			break
		}
	}
	// A head that is only the previous summary has nothing new in it.
	if keep == 0 {
		return out
	}
	if h, _ := splitForCompaction(out, keep); len(h) <= repl {
		return out
	}

	if c.s.callbacks.OnStatus != nil {
		c.s.callbacks.OnStatus("Compacting conversation…")
	}
	// out is already the pruned request, so the summary sees it as it is.
	summary, head, tail, err := c.s.summarizeFitting(c.ctx, out, keep, func(m []openai.ChatCompletionMessage) []openai.ChatCompletionMessage { return m })
	if err != nil {
		xlog.Warn("mid-turn compaction failed", "error", err)
		c.failed = true
		return out
	}
	// The overflow retries may have moved the boundary back until the head
	// is only the previous summary, which has nothing new in it.
	if len(head) <= repl {
		return out
	}
	// Save the head actually summarized as a compaction artifact, the same
	// as the end-of-turn path, so nothing is lost to the lossy summary.
	artifactURI := c.s.spillCompactionHead(renderMessages(head))

	// renderMessages leaves system messages out of the summary, so they are
	// kept as they are: dropping them would lose the system prompt.
	var replacement []openai.ChatCompletionMessage
	for _, m := range head {
		if m.Role == "system" {
			replacement = append(replacement, m)
		}
	}
	replacement = append(replacement, summaryMessage(summary, artifactURI))

	covered := base + len(head) - repl
	c.covered, c.last, c.replacement = covered, msgs[covered-1], replacement

	compacted := append(append([]openai.ChatCompletionMessage(nil), replacement...), tail...)
	// The live figure measured the history that was just replaced; see
	// compactHistory.
	c.s.live.reset()
	if c.s.callbacks.OnCompactDone != nil {
		c.s.callbacks.OnCompactDone(estimateTokens(out), estimateTokens(compacted))
	}
	return compacted
}

// apply rewrites f, a fragment a run returned, the way the run's requests were
// rewritten. It returns the kept tail too, for the display copy, and false when
// the run did not compact or f does not start with the summarized history.
func (c *turnCompactor) apply(f cogito.Fragment) (cogito.Fragment, []openai.ChatCompletionMessage, bool) {
	if !c.matches(f.Messages) {
		return f, nil, false
	}
	tail := f.Messages[c.covered:]
	msgs := make([]openai.ChatCompletionMessage, 0, len(c.replacement)+len(tail))
	f.Messages = append(append(msgs, c.replacement...), tail...)
	return f, tail, true
}

// commitRun stores f, a fragment a run returned, as the session's history, in
// the compacted form when the run compacted, and adds display to the display
// copy.
//
// Every path that keeps a run's fragment goes through here. Keeping the raw one
// would start the next run or turn from the history the summary replaced, which
// is the size that needed compacting. display is added before the display copy
// is rebuilt, so the rebuild counts it, and under the same lock, so a reader
// never sees one copy updated without the other.
func (s *Session) commitRun(c *turnCompactor, f cogito.Fragment, display ...openai.ChatCompletionMessage) {
	f, tail, compacted := c.apply(f)
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	s.fragment = f
	s.messages = append(s.messages, display...)
	if compacted {
		s.messages = compactedDisplay(s.messages, tail)
	}
}
