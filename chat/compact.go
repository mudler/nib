package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/mudler/nib/types"

	"github.com/mudler/cogito"
	openai "github.com/sashabaranov/go-openai"
)

// compactInstruction is the prompt prefix used to summarize the older portion
// of a conversation during compaction. The structured template forces the model
// to organize information into durable categories (goal, instructions,
// discoveries, accomplished, relevant files, open work) rather than producing a
// flat narrative that buries file paths and decisions in prose.
const compactInstruction = "You are compacting a conversation to save context. " +
	"Provide a detailed summary for continuing the conversation. " +
	"Do not answer any questions in the conversation — output only the summary.\n\n" +
	"Stick to this template:\n" +
	"---\n" +
	"## Goal\n" +
	"[What goal(s) is the user trying to accomplish?]\n\n" +
	"## Instructions\n" +
	"- [Important instructions the user gave that are relevant]\n" +
	"- [If there is a plan or spec, include information about it]\n\n" +
	"## Discoveries\n" +
	"[Notable things learned: architecture, file locations, patterns, gotchas, anything non-obvious]\n\n" +
	"## Accomplished\n" +
	"[What work has been completed, what is still in progress, what is left?]\n\n" +
	"## Relevant files / directories\n" +
	"[Structured list of relevant files that have been read, edited, or created, with line numbers where relevant]\n\n" +
	"## Open work\n" +
	"[Remaining tasks, unresolved questions, and next steps]\n" +
	"---\n\n" +
	"Preserve all file paths, identifiers, and decisions exactly. " +
	"Be concise but complete."

// splitForCompaction partitions msgs into a head (to be summarized) and a tail
// (kept verbatim). keepRecent is the desired tail length; the boundary is moved
// backward so an assistant tool_calls message is never separated from its tool
// result messages (which would make the next API call invalid). It returns
// (nil, nil) when there is nothing worth compacting.
func splitForCompaction(msgs []openai.ChatCompletionMessage, keepRecent int) (head, tail []openai.ChatCompletionMessage) {
	if keepRecent < 1 {
		keepRecent = 1
	}
	start := len(msgs) - keepRecent
	if start < 1 {
		return nil, nil
	}
	// Never begin the tail on a tool result, and never leave a dangling
	// assistant tool_calls at the end of the head.
	for start > 0 && (msgs[start].Role == "tool" || len(msgs[start-1].ToolCalls) > 0) {
		start--
	}
	if start < 1 {
		return nil, nil
	}
	return msgs[:start], msgs[start:]
}

// shouldAutoCompact reports whether the last request's prompt tokens crossed the
// configured fraction of the BUDGET — the window minus the reserve. Auto-
// compaction is off when Disabled or when no window is available (configured or
// learned).
//
// The window is passed in rather than read from cfg because it may have been
// learned from the backend, which is more trustworthy than any configured
// guess.
func shouldAutoCompact(cfg types.CompactionConfig, window, promptTokens int) bool {
	if cfg.Disabled || window <= 0 {
		return false
	}
	budget := ContextBudget(cfg, window)
	// Defensive only: ContextBudget clamps the reserve to half of the
	// window, so a positive window always has a positive budget and the
	// check above already rejected the rest. It stays because a zero budget
	// would make the trigger fire on every single turn, which is the one
	// outcome worse than never firing.
	if budget <= 0 {
		return false
	}
	threshold := cfg.Threshold
	if threshold <= 0 || threshold > 1 {
		threshold = 0.8
	}
	return promptTokens >= int(float64(budget)*threshold)
}

// minPlausibleWindow and maxPlausibleWindow bound what rememberWindow will
// believe. The band rejects a MISPARSE, not a small model: a 2048- or
// 4096-context model is exactly nib's audience, so the floor sits below any
// real served model and above any plausible misparse of an output-token count
// (the largest default max_tokens backends print in overflow errors is in the
// hundreds — vLLM's "512 output tokens" is the known example; the overflow
// catalog's named groups keep it out of the window, see overflow_patterns.go).
//
// The ceiling exists because strconv.Atoi clamps an absurdly long digit run to
// MaxInt instead of erroring, so garbage lands far ABOVE any floor rather than
// below it. 1<<30 is roughly a hundred times the largest window any model has
// ever advertised (~10M tokens), so it cannot reject a real backend, while
// still catching the clamp.
const (
	minPlausibleWindow = 1024
	maxPlausibleWindow = 1 << 30
)

// defaultReserveTokens is the fallback for an unset CompactionConfig.
// ReserveTokens. It must match config.Load's default (config cannot be imported
// from here — chat is downstream of it — so the number is repeated, as the 0.8
// Threshold default already is).
const defaultReserveTokens = 4096

// defaultSummaryMaxTokens is the fallback for an unset CompactionConfig.
// SummaryMaxTokens, repeated from config.Load for the same reason.
const defaultSummaryMaxTokens = 16384

// maxSummaryAttempts bounds how many summary requests summarizeFitting sends
// for one compaction. Each overflow moves the boundary so the head shrinks by
// the backend's stated overshoot, so a fit normally takes one or two retries.
const maxSummaryAttempts = 6

// rememberWindow records a context window a backend stated for a specific
// model. Values outside the plausibility band are discarded rather than
// stored, because an implausible figure is a parse artefact and storing one
// would be worse than the configured guess it replaced: too small and
// compaction fires every turn, too large and it never fires at all.
func (s *Session) rememberWindow(window int, model string) {
	if window < minPlausibleWindow || window > maxPlausibleWindow || model == "" {
		return
	}
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	s.learnedWindow, s.learnedWindowModel = window, model
}

// contextWindow returns the window compaction budgets against: the learned one
// when it belongs to the model currently in use, otherwise the configured
// MaxContextTokens.
//
// Keyed on the model NAME rather than on whichever method changed the model.
// A window learned from one backend's overflow error is a fact about ONE
// model, so it is meaningless except read as a pair with llmModel: carrying a
// 262k window into an 8k model would suppress compaction precisely when it is
// most needed. Comparing the names makes that safe for every path that can
// change the model, present or future, without anyone having to remember to
// clear the field from it. (Today only NewSession and SetModel assign
// llmModel; Reload does not.)
func (s *Session) contextWindow() int {
	s.modelMu.RLock()
	defer s.modelMu.RUnlock()
	return s.windowLocked()
}

// windowLocked is contextWindow's body, for callers that already hold modelMu.
//
// The lock has to span the MaxContextTokens read as well as the learned pair:
// SetModel writes that field under modelMu when it re-detects the window for a
// new model, and it runs on whichever goroutine drives the UI while these
// readers run on the turn goroutine. Reading it outside the lock was a data
// race — benign in practice, since an int does not tear on the platforms nib
// targets, but a race the detector flags and the memory model does not permit.
func (s *Session) windowLocked() int {
	if s.learnedWindow > 0 && s.learnedWindowModel == s.llmModel {
		return s.learnedWindow
	}
	return s.compaction.MaxContextTokens
}

// shouldCompactNow reports whether the last request's prompt tokens crossed the
// auto-compaction trigger.
//
// It exists so the policy and the window are read under ONE lock. Taking them
// separately would let a model switch land between the two and pair a new
// model's window with the previous policy — and it would leave the whole-struct
// copy of s.compaction unguarded against SetModel's write.
func (s *Session) shouldCompactNow(promptTokens int) bool {
	s.modelMu.RLock()
	cfg, window := s.compaction, s.windowLocked()
	s.modelMu.RUnlock()
	return shouldAutoCompact(cfg, window, promptTokens)
}

// ContextWindow reports the context window this session is actually budgeting
// against: the one learned from a backend overflow error when it belongs to the
// model in use, otherwise the configured MaxContextTokens.
//
// It exists for display. The TUI cannot read s.compaction.MaxContextTokens and
// call it the window, because a learned window silently replaces it — a session
// configured for 400k against a model that really serves 262k would draw a
// badge claiming plenty of room while compaction fires.
func (s *Session) ContextWindow() int {
	return s.contextWindow()
}

// canRecoverFromOverflow reports whether a failed run's error is one nib may
// act on: a context overflow the user did not cancel.
//
// The cancellation half is not redundant with isContextOverflow. Today cogito's
// retry loop calls backoffOrCancel after every failed attempt and returns
// ctx.Err() when the context is done, so an interrupt that lands during the
// request reaches here as "context canceled" and fails the overflow check
// anyway. That is cogito's mapping, not nib's guarantee: returning the last
// real error instead would be a perfectly reasonable change, and cancellation
// can also land in the window between ExecuteTools returning an overflow and
// this check running. A cancelled turn means the user pressed Ctrl+C, and
// re-sending the turn is the opposite of what they asked for — so the rule is
// stated here rather than inferred from another package's error mapping.
//
// It is a free function rather than a method because it reads no session state:
// that keeps the boundary testable without a live turn, which is the only way
// the interrupt case can be exercised at all (see the note above).
func canRecoverFromOverflow(turnCtx context.Context, err error) bool {
	return turnCtx.Err() == nil && isContextOverflow(err)
}

// canShrinkSummary reports whether a rejected summary request is worth
// retrying with a smaller prompt: a context overflow, or a budget overflow
// (the prompt fits but prompt + reserved output does not), the user did not
// cancel. The summary's output reservation is already capped, so shrinking
// its prompt is what fits a budget overflow here; this is not compaction of
// the conversation, which a budget overflow must never trigger.
func canShrinkSummary(ctx context.Context, err error) bool {
	return ctx.Err() == nil && isWindowOverflow(err)
}

// overflowRetries reports how many context-overflow recoveries the current turn
// performed. Read by tests; the cap itself is enforced in SendMessage.
func (s *Session) overflowRetries() int {
	s.overflowMu.Lock()
	defer s.overflowMu.Unlock()
	return s.overflowRetried
}

// turnRetries reports how many times the current turn was run again after a
// rate-limited or transient backend error. Read by tests; the budget itself
// is enforced in SendMessage.
func (s *Session) turnRetries() int {
	s.turnRetryMu.Lock()
	defer s.turnRetryMu.Unlock()
	return s.turnRetryTotal
}

// ContextBudget is the window minus the reserve held back for the response,
// where the reserve is never allowed to claim more than half of the window.
//
// Exported so the TUI's context badge can be drawn against the same number
// auto-compaction triggers on. A badge that budgeted against the raw window
// disagreed with the moment compaction actually fires, which is the one thing
// the badge exists to predict.
//
// The clamp is not the percentage reserve the spec rejected. The reserve stays
// a flat cfg.ReserveTokens for every window of 2×ReserveTokens or more — 8192
// and up at the 4096 default — and the trigger is still Threshold × budget, not
// a percentage of the window. The clamp bites only where the flat number is
// incoherent relative to the window it is being subtracted from: without it a
// 4096-token model reserves its entire window, the budget is 0, and auto-
// compaction switches OFF for the model that overflows soonest. Worse, once a
// window is learned from an overflow error, LEARNING a real 4096 window would
// be what disabled compaction for the model that just overflowed — the exact
// inverse of the point of learning it.
//
// The ceiling is half the window rather than a quarter so that an explicitly
// large reserve can hold back up to half the context for the model's response
// after compaction — matching maki's MAX_RESERVED_PERCENT. A model that just
// hit compaction may need to emit a long response (a rewritten file, a detailed
// plan), and the larger ceiling gives that response room. The flat 4096 default
// is untouched on any window of 8192 or more, so only small windows or
// deliberately large reserves are affected.
//
// The default is applied HERE as well as in config.Load, the same way
// shouldAutoCompact defaults Threshold at its use site. An embedder calling
// chat.NewSession directly never passes through config.Load, and an unset
// ReserveTokens would then reserve nothing — which is precisely the failure
// this budget exists to prevent, arriving through the one door nobody watches.
//
// It lands BEFORE the half-window clamp, so the two stay coherent: a small
// window still clamps the default (a 4096-token model reserves 2048, not 4096)
// rather than the clamp being bypassed by a zero.
func ContextBudget(cfg types.CompactionConfig, window int) int {
	reserve := cfg.ReserveTokens
	if reserve <= 0 {
		reserve = defaultReserveTokens
	}
	reserve = min(reserve, window/2)
	b := window - reserve
	if b < 0 {
		return 0
	}
	return b
}

// estimateTokens is a cheap byte/4 approximation, used when no real usage figure
// is available (e.g. right after a rebuild, before the next live turn). It only
// counts m.Content and tool calls, ignoring m.MultiContent (multimedia parts).
func estimateTokens(msgs []openai.ChatCompletionMessage) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
		for _, tc := range m.ToolCalls {
			n += len(tc.Function.Name) + len(tc.Function.Arguments)
		}
	}
	return n / 4
}

// estimateUsageSplit is estimateTokens sliced by role: the assistant's own
// messages count toward completion, everything else (user, system, tool
// results) toward prompt. Used by Session.EstimatedUsage as the fallback for
// a streamed session whose real counter reports zero. Character totals are
// summed per side before the /4 division, matching estimateTokens's own
// method rather than rounding each message independently.
func estimateUsageSplit(msgs []openai.ChatCompletionMessage) (prompt, completion int) {
	var promptChars, completionChars int
	for _, m := range msgs {
		chars := len(m.Content)
		for _, tc := range m.ToolCalls {
			chars += len(tc.Function.Name) + len(tc.Function.Arguments)
		}
		if m.Role == "assistant" {
			completionChars += chars
		} else {
			promptChars += chars
		}
	}
	return promptChars / 4, completionChars / 4
}

// ContextTokens reports the current conversation size in tokens for display:
// the last request's reported prompt tokens, or a byte/4 estimate when the
// backend hasn't reported usage yet (e.g. before the first turn). This is the
// same signal the auto-compaction trigger watches.
func (s *Session) ContextTokens() int {
	// A turn in flight is the authority over its own size: s.fragment still
	// holds the PREVIOUS turn's Status until ExecuteTools returns, so reading
	// it here froze the gauge for the whole turn — a multi-step turn could add
	// fifty thousand tokens without the footer moving. See liveUsage.
	if n := s.live.promptTokens(); n > 0 {
		return n
	}
	// The fallback reads the fragment, which the turn goroutine reassigns at
	// each run's end — and the UI polls this once a second now, not only at
	// turn boundaries, so the read takes the lock that guards it.
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	if s.fragment.Status != nil && s.fragment.Status.LastUsage.PromptTokens > 0 {
		return s.fragment.Status.LastUsage.PromptTokens
	}
	return estimateTokens(s.fragment.Messages)
}

// MaxContextTokens returns the CONFIGURED window: the user's explicit
// max_context_tokens when they set one, otherwise the auto-detected value from
// the endpoint probe or static table, falling back to the 128k default.
//
// It is not the window the session budgets against — use ContextWindow for
// that. A window learned from a backend overflow error replaces this value for
// the model it was learned on, so the two disagree exactly when the configured
// number was wrong about the model, which is the case worth knowing about.
func (s *Session) MaxContextTokens() int {
	s.modelMu.RLock()
	defer s.modelMu.RUnlock()
	return s.compaction.MaxContextTokens
}

// formatTokenCount returns the bare magnitude string for a token count, e.g.
// 950 → "950", 12000 → "12k", 47200 → "47.2k". A trailing ".0" is trimmed.
// Returns "" for zero/negative so callers can omit the segment.
func formatTokenCount(n int) string {
	if n <= 0 {
		return ""
	}
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	s := fmt.Sprintf("%.1f", float64(n)/1000.0)
	s = strings.TrimSuffix(s, ".0")
	return s + "k"
}

// compactedNotice is the line inserted into the visible transcript when older
// turns are summarized. It has no emoji: it renders in the TUI exactly like a
// render helper, and the calm no-emoji voice tui.TestNoEmojiInRenderHelpers
// guards applies to it even though that test cannot see it from here. Its own
// guard is TestCompactionTranscriptNoticeHasNoEmoji, in this package.
//
// It counts messages, not tokens, so unlike the CLI and TUI compaction notices
// it has nothing approximate to mark: len() of a slice is exact.
func compactedNotice(removed int) string {
	return fmt.Sprintf("Compacted %d earlier messages", removed)
}

// HumanTokens formats a token count compactly (e.g. 47200 → "47.2k").
func HumanTokens(n int) string {
	return formatTokenCount(n)
}

// summaryPiece is one message rendered for the summarization prompt.
type summaryPiece struct {
	role string
	text string
	// tool marks a tool result or a message that calls tools.
	tool bool
}

// renderMessages flattens messages to plain "role: content" lines for the
// summarization prompt, one piece per message, skipping system boilerplate and
// rendering tool calls inline. A message carrying both content and tool calls
// renders both. Like estimateTokens, this ignores m.MultiContent (multimedia
// parts).
func renderMessages(msgs []openai.ChatCompletionMessage) []summaryPiece {
	var out []summaryPiece
	for _, m := range msgs {
		if m.Role == "system" {
			continue
		}
		var b strings.Builder
		if m.Content != "" {
			fmt.Fprintf(&b, "%s: %s\n", m.Role, m.Content)
		}
		for _, tc := range m.ToolCalls {
			fmt.Fprintf(&b, "%s: [tool call %s(%s)]\n", m.Role, tc.Function.Name, tc.Function.Arguments)
		}
		if b.Len() > 0 {
			out = append(out, summaryPiece{role: m.Role, text: b.String(), tool: m.Role == "tool" || len(m.ToolCalls) > 0})
		}
	}
	return out
}

// stubbedView returns a copy of msgs with every result in already replaced by
// the stub the requests send in its place.
//
// Compaction must summarize this view, not the raw fragment. The fragment keeps
// the full body of every stubbed result, so on a long session it grows far past
// the window while every request still fits. Summarizing the raw fragment then
// sends the one request that cannot fit, and compaction fails exactly when the
// session needs it.
func stubbedView(msgs []openai.ChatCompletionMessage, already map[string]string) []openai.ChatCompletionMessage {
	if len(already) == 0 {
		return msgs
	}
	calls := indexToolCalls(msgs)
	out := make([]openai.ChatCompletionMessage, len(msgs))
	copy(out, msgs)
	for i, m := range out {
		detail, ok := already[m.ToolCallID]
		if m.Role != "tool" || !ok {
			continue
		}
		info := calls[m.ToolCallID]
		if info.name == "" {
			info.name = "tool"
		}
		out[i].Content = prunedStub(info.name, info.path, detail)
	}
	return out
}

// summaryHeadBudget and summaryTailBudget are how much of the head and tail of
// an over-long piece fitSummaryInput keeps: the start of a message carries its
// role, the tool name and its arguments (which records that the step happened),
// while the tail carries trailing results a summary may still need.
const (
	summaryHeadBudget = 256
	summaryTailBudget = 256
)

// fitSummaryInput joins pieces into at most maxTokens (byte/4) tokens. A
// maxTokens of zero or less means no limit.
//
// It gives things up in order of what a summary can best spare. First it cuts
// long tool results and tool calls, oldest first: the model re-reads files
// anyway, and a cut piece still records which tool ran on what. Then it cuts
// long user and assistant messages, oldest first. Last, it drops whole pieces
// after the first, oldest first, because the first is normally the user's
// statement of the goal.
func fitSummaryInput(pieces []summaryPiece, maxTokens int) string {
	join := func() string {
		var b strings.Builder
		for _, p := range pieces {
			b.WriteString(p.text)
		}
		return b.String()
	}
	total := 0
	for _, p := range pieces {
		total += len(p.text)
	}
	if maxTokens <= 0 || total/4 <= maxTokens {
		return join()
	}
	pieces = append([]summaryPiece(nil), pieces...)
	limit := maxTokens * 4

	cut := func(tools bool) {
		for i := range pieces {
			if total <= limit {
				return
			}
			if pieces[i].tool != tools || len(pieces[i].text) <= summaryHeadBudget+summaryTailBudget {
				continue
			}
			text := pieces[i].text
			if len(text) <= summaryHeadBudget+summaryTailBudget {
				continue
			}
			head := text[:summaryHeadBudget]
			tail := text[len(text)-summaryTailBudget:]
			short := head + fmt.Sprintf("\n[... %d bytes omitted to fit the summary]\n", len(text)-summaryHeadBudget-summaryTailBudget) + tail
			total -= len(text) - len(short)
			pieces[i].text = short
		}
	}
	cut(true)
	cut(false)

	dropped := 0
	for total > limit && len(pieces) > 1 {
		total -= len(pieces[1].text)
		pieces = append(pieces[:1], pieces[2:]...)
		dropped++
	}
	if dropped > 0 {
		marker := fmt.Sprintf("[... %d earlier messages omitted to fit the summary]\n", dropped)
		pieces = append(pieces[:1], append([]summaryPiece{{text: marker}}, pieces[1:]...)...)
	}
	return join()
}

// summaryRetryTarget returns the prompt size to retry a summary at after the
// backend rejected a prompt of sent (byte/4) tokens.
//
// The rejection states the request size and the window in the backend's own
// count. Their difference is by how many tokens the request overshot, and that
// is a fixed number, not a ratio: the output the request reserves does not
// shrink with the prompt. Scaling the prompt by window/request left the
// reservation whole, so a large reservation overflowed again on every retry.
// The 10% margin covers the byte/4 estimate varying across the text. With no
// figures to go on, it halves. A result of zero or less means the reservation
// alone does not fit, and no prompt will.
func summaryRetryTarget(err error, sent int) int {
	needs, allows, ok := overflowFigures(err)
	if !ok {
		return sent / 2
	}
	return int(float64(sent-(needs-allows)) * 0.9)
}

// overflowFigures reads the request size and the window from an overflow
// error for the subtractive summary retry: (Total, Window) from the catalog
// row, with Input + Output (or Input alone) standing in for a missing total.
// A context or a budget overflow qualifies; a generic wording states no
// figures and reports ok == false, so the caller halves instead.
func overflowFigures(err error) (needs, allows int, ok bool) {
	info := classifyOverflow(err)
	if info.Kind != KindContext && info.Kind != KindBudget {
		return 0, 0, false
	}
	needs, allows = info.overflowNeeds(), info.Window
	if needs <= 0 || allows <= 0 {
		return 0, 0, false
	}
	return needs, allows, true
}

// CompactHistory summarizes the older portion of the conversation via the LLM
// and rebuilds the fragment as [summary] + recent tail, keeping the display
// copy consistent. It returns byte/4 token estimates of the conversation before
// and after compaction (before==after signals a no-op). On summary failure it
// returns the error WITHOUT mutating session state (atomic swap).
func (s *Session) CompactHistory() (before, after int, err error) {
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s.compactHistory(ctx)
}

// compactHistory is the context-aware implementation of CompactHistory. The
// passed-in ctx governs the summarization LLM call, allowing callers (e.g.
// auto-compaction) to make it cancellable via a per-turn context.
func (s *Session) compactHistory(ctx context.Context) (before, after int, err error) {
	return s.compactHistoryKeep(ctx, s.compactionConfig().KeepRecent)
}

// compactHistoryKeep is compactHistory with the tail length passed in rather
// than read from the config. Overflow recovery uses it to retry with a
// smaller tail without writing the shared CompactionConfig.
func (s *Session) compactHistoryKeep(ctx context.Context, keep int) (before, after int, err error) {
	msgs := s.fragment.Messages

	before = estimateTokens(msgs)

	s.prunedMu.Lock()
	pruned := make(map[string]string, len(s.prunedIDs))
	for k, v := range s.prunedIDs {
		pruned[k] = v
	}
	compressed := s.copyCompressedLocked()
	s.prunedMu.Unlock()
	// The summary sees what the requests saw: stubs, then the progressive
	// compression levels (see progressivePrune).
	view := func(m []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
		if len(compressed) == 0 {
			return stubbedView(m, pruned)
		}
		return requestView(m, pruned, compressed)
	}
	summary, head, tail, err := s.summarizeFitting(ctx, msgs, keep, view)
	if err != nil {
		return before, before, err
	}
	if len(head) == 0 {
		return before, before, nil // nothing to compact
	}
	artifactURI := s.spillCompactionHead(renderMessages(view(head)))

	// Build the new state up front; swap only after success (atomic).
	newFragMsgs := append([]openai.ChatCompletionMessage{summaryMessage(summary, artifactURI)}, tail...)

	s.historyMu.Lock()
	newMessages := compactedDisplay(s.messages, tail)
	newFrag := cogito.NewFragment(newFragMsgs...)
	if s.fragment.Status != nil {
		// Preserve the running token counters — but NOT LastUsage, which
		// measured a request against the conversation this one just replaced.
		// Left in place it outlives the history it describes: ContextTokens
		// would keep reporting the pre-compaction size, so the footer's own
		// "compacted 180k → 40k" notice would be contradicted by the gauge
		// beside it. Zeroed, the fragment's estimate answers instead — the
		// same number the notice quotes — until the next request reports a
		// real one. The copy keeps this off the Status a reader may hold.
		statusCopy := *s.fragment.Status
		statusCopy.LastUsage = cogito.LLMUsage{}
		newFrag.Status = &statusCopy
	}
	s.fragment = newFrag
	s.messages = newMessages
	s.historyMu.Unlock()
	// Same reason, for a turn still in flight: the live figure was measured
	// against the history that just went away.
	s.live.reset()

	after = estimateTokens(newFrag.Messages)
	return before, after, nil
}

// summaryPrefix is the instruction the rendered conversation follows in the
// summary prompt.
const summaryPrefix = compactInstruction + "\n\n--- CONVERSATION ---\n"

// summaryOutputTokens is the output the summary request reserves: the smaller
// of the model's output cap and cfg.SummaryMaxTokens, and never more than half
// of a known window, so the prompt keeps room beside it.
//
// It is set on the request explicitly. Left to the client, the request
// reserves the model's whole output cap, and a backend that checks prompt +
// reservation against the window rejects every summary prompt larger than the
// window minus that cap, however short the summary would have been.
func (s *Session) summaryOutputTokens() int {
	cfg := s.compactionConfig()
	out := cfg.SummaryMaxTokens
	if out <= 0 {
		out = defaultSummaryMaxTokens
	}
	cap, window := s.requestLimits()
	if cap > 0 {
		out = min(out, cap)
	}
	if window > 0 {
		out = max(min(out, window/2), 1)
	}
	return out
}

// summaryPromptLimit is the budget, in byte/4 tokens, for the rendered
// conversation in the first summary request: the window less the summary's
// output reservation (and never more than ContextBudget), less the
// instruction. 0 means no window is known, so no limit.
func (s *Session) summaryPromptLimit(output int) int {
	window := s.contextWindow()
	if window <= 0 {
		return 0
	}
	budget := min(ContextBudget(s.compactionConfig(), window), window-output)
	return max(budget-tokensOf(summaryPrefix), 1)
}

// summarizeFitting summarizes the head of msgs that splitForCompaction(msgs,
// keep) leaves, and returns the summary with the head it covers and the tail
// to keep verbatim. view maps messages to what the summary prompt should see
// (the pruned view of them). An empty head, with a nil error, means there was
// nothing to compact.
//
// The whole head goes out in one request first. When the backend rejects it
// as too large, the head is summarized in chunks instead (see rollingCover),
// each fitted to the retry target summaryRetryTarget derives from the
// rejection. When the chunks cover the whole head, the result is the summary
// and the original tail. When they stop at maxRollingChunks, the head ends
// where they stopped and the rest joins the tail, verbatim: nothing is lost,
// and the next compaction can summarize it. The loop stops on a non-overflow
// error, a cancelled context, a target of zero or less, or after
// maxSummaryAttempts overflows in a row, and returns the last error. It
// changes no session state other than the usage it counts.
func (s *Session) summarizeFitting(ctx context.Context, msgs []openai.ChatCompletionMessage, keep int, view func([]openai.ChatCompletionMessage) []openai.ChatCompletionMessage) (summary string, head, tail []openai.ChatCompletionMessage, err error) {
	head, tail = splitForCompaction(msgs, keep)
	if len(head) == 0 || len(renderMessages(view(head))) == 0 {
		return "", nil, nil, nil
	}
	limit := s.summaryPromptLimit(s.summaryOutputTokens())
	summary, sent, serr := s.summarize(ctx, renderMessages(view(head)), limit)
	if serr == nil {
		return summary, head, tail, nil
	}
	if maxSummaryAttempts <= 1 || !canShrinkSummary(ctx, serr) {
		return "", nil, nil, serr
	}
	target := summaryRetryTarget(serr, sent)
	if target <= 0 {
		return "", nil, nil, serr
	}
	summary, covered, err := s.rollingCover(ctx, head, view, target, 1)
	if err != nil {
		return "", nil, nil, err
	}
	// head is msgs[:len(head)], so what the chunks did not cover and the
	// original tail are one contiguous run of msgs.
	return summary, msgs[:covered], msgs[covered:], nil
}

// maxRollingChunks bounds how many chunks rollingCover summarizes for one
// compaction. What is left after that stays verbatim.
const maxRollingChunks = 8

// rollingInstruction is the instruction for every chunk after the first: the
// running summary comes first in the prompt, the chunk's messages after it.
const rollingInstruction = compactInstruction + "\n\n" +
	"A summary of the earlier part of this conversation is given first. " +
	"Merge it with the new messages into one summary in the same format; " +
	"keep every file path, identifier and decision from both."

// rollingPrefix is the prompt a later chunk's rendered messages follow.
func rollingPrefix(running string) string {
	return rollingInstruction + "\n\n--- EARLIER SUMMARY ---\n" + running + "\n\n--- NEW MESSAGES ---\n"
}

// rollingSummary summarizes head in consecutive chunks, each fitted to a
// prompt of target byte/4 tokens, and returns the one summary that stands
// for all of head. It fails when maxRollingChunks chunks do not cover head;
// summarizeFitting, which keeps the uncovered rest verbatim instead, uses
// rollingCover directly.
func (s *Session) rollingSummary(ctx context.Context, head []openai.ChatCompletionMessage, view func([]openai.ChatCompletionMessage) []openai.ChatCompletionMessage, target int) (summary string, err error) {
	summary, covered, err := s.rollingCover(ctx, head, view, target, 0)
	if err != nil {
		return "", err
	}
	if covered < len(head) {
		return "", fmt.Errorf("compaction summary covered %d of %d messages in %d chunks", covered, len(head), maxRollingChunks)
	}
	return summary, nil
}

// rollingCover summarizes head from oldest to newest in chunks, and returns
// the running summary and how many of head's messages it stands for.
//
// Each chunk is the longest run that fits target: the first after the
// compaction instruction, each later one after rollingInstruction and the
// running summary, whose reply then replaces the running summary. A chunk
// ends only where splitForCompaction could, so it never separates a tool
// call from its results. A run too large for target on its own still forms
// a chunk, and fitSummaryInput cuts it down. When the backend rejects a chunk
// anyway, target drops by the stated overshoot (summaryRetryTarget) and the
// chunk is rebuilt from the same start, so the rest moves on to the next
// chunk. failures is how many overflows in a row the caller already had;
// maxSummaryAttempts of them in a row stop it. It stops after
// maxRollingChunks chunks, and on any error it returns nothing covered.
func (s *Session) rollingCover(ctx context.Context, head []openai.ChatCompletionMessage, view func([]openai.ChatCompletionMessage) []openai.ChatCompletionMessage, target, failures int) (summary string, covered int, err error) {
	viewed := view(head)
	if len(viewed) != len(head) {
		return "", 0, fmt.Errorf("compaction view changed the message count")
	}
	sizes := make([]int, len(viewed))
	for i := range viewed {
		for _, p := range renderMessages(viewed[i : i+1]) {
			sizes[i] += len(p.text)
		}
	}
	boundary := func(i int) bool {
		return i == len(head) || (head[i].Role != "tool" && len(head[i-1].ToolCalls) == 0)
	}

	running := ""
	start, chunks := 0, 0
	for start < len(head) && chunks < maxRollingChunks {
		prefix := summaryPrefix
		if chunks > 0 {
			prefix = rollingPrefix(running)
		}
		budget := target - tokensOf(prefix)
		end, acc := 0, 0
		for e := start + 1; e <= len(head); e++ {
			acc += sizes[e-1]
			if !boundary(e) {
				continue
			}
			if end == 0 || acc/4 <= budget {
				end = e
			}
			if acc/4 > budget {
				break
			}
		}
		pieces := renderMessages(viewed[start:end])
		if len(pieces) == 0 {
			start = end // nothing in it for a summary to cover
			continue
		}
		reply, sent, serr := s.summarizeWith(ctx, prefix, pieces, max(budget, 1))
		if serr == nil {
			running, start, failures = reply, end, 0
			chunks++
			continue
		}
		failures++
		if failures >= maxSummaryAttempts || !canShrinkSummary(ctx, serr) {
			return "", 0, serr
		}
		if target = summaryRetryTarget(serr, sent); target <= 0 {
			return "", 0, serr
		}
	}
	if chunks == 0 {
		return "", 0, fmt.Errorf("compaction produced an empty summary")
	}
	return running, start, nil
}

// spillCompactionHead saves the rendered head a summary stands for as an
// artifact, so nothing is lost to the lossy summary, and returns its URI ("" when
// spilling is off or there is no store). The model can page through it with
// the read tool (artifact://N) or search it with search_artifacts.
func (s *Session) spillCompactionHead(pieces []summaryPiece) string {
	if s.compactionConfig().DisableArtifactSpill || s.artifacts == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range pieces {
		b.WriteString(p.text)
	}
	if b.Len() == 0 {
		return ""
	}
	return s.artifacts.Save("compaction", b.String())
}

// summarize sends one summary request for pieces, fitted into limit byte/4
// tokens (0 means no limit) after the instruction, and returns the summary and
// the byte/4 size of the prompt it sent. It counts the spend of the call.
//
// The request goes out through CreateChatCompletion with MaxTokens set to
// summaryOutputTokens. Ask would leave the reservation to the client, which
// reserves the model's whole output cap.
func (s *Session) summarize(ctx context.Context, pieces []summaryPiece, limit int) (summary string, sentTokens int, err error) {
	return s.summarizeWith(ctx, summaryPrefix, pieces, limit)
}

// summarizeWith is summarize with the instruction prefix the rendered pieces
// follow in the prompt.
func (s *Session) summarizeWith(ctx context.Context, prefix string, pieces []summaryPiece, limit int) (summary string, sentTokens int, err error) {
	prompt := prefix + fitSummaryInput(pieces, limit)
	sentTokens = tokensOf(prompt)
	llm, _ := s.currentLLM()
	reply, usage, aerr := llm.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Messages:  []openai.ChatCompletionMessage{{Role: cogito.UserMessageRole.String(), Content: prompt}},
		MaxTokens: s.summaryOutputTokens(),
	})
	// Compaction is not free, and it fires exactly when a session has already
	// grown expensive — so leaving it out would understate the runs that cost
	// the most.
	//
	// Counted before BOTH exits below on purpose. What the backend served it
	// billed, whether the summary then came back empty or the call came back an
	// error, and a rejected summary must not also erase the spend — the same
	// rule the interrupted-turn path in SendMessage follows. A client that
	// reports nothing on error simply adds zero.
	s.addUsage(usage)
	if aerr != nil {
		return "", sentTokens, fmt.Errorf("compaction summary failed: %w", aerr)
	}
	var content string
	if ch := reply.ChatCompletionResponse.Choices; len(ch) > 0 {
		content = ch[0].Message.Content
	}
	if strings.TrimSpace(content) == "" {
		return "", sentTokens, fmt.Errorf("compaction produced an empty summary")
	}
	return content, sentTokens, nil
}

// summaryMessage wraps a compaction summary as the message that stands in for
// the history it summarizes.
//
// The continuation instruction mirrors maki's CONTINUE_AFTER_COMPACT: it
// tells the model to re-orient from the structured summary before
// continuing, so the compaction boundary does not silently drop context.
// The memory tool reference nudges the model to persist durable facts
// (paths, decisions, gotchas) that the lossy summary may not preserve.
func summaryMessage(summary, artifactURI string) openai.ChatCompletionMessage {
	content := "[Earlier conversation compacted. Review the summary below and continue from where you left off. " +
		"If the summary contains important context that should persist across sessions, save it to memory now before it is lost.]\n\n" + summary
	if artifactURI != "" {
		content += "\n\nFull conversation before compaction is available at " + artifactURI +
			" — use the read tool with this path to page through it, or use search_artifacts to search for specific content."
	}
	return openai.ChatCompletionMessage{
		Role:    "user",
		Content: content,
	}
}

// compactedDisplay rebuilds the display copy after compaction: a notice
// counting what went, then the user and assistant text of the kept tail.
func compactedDisplay(displayed, tail []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	displayTail := []openai.ChatCompletionMessage{}
	for _, m := range tail {
		if (m.Role == "user" || m.Role == "assistant") && strings.TrimSpace(m.Content) != "" {
			displayTail = append(displayTail, openai.ChatCompletionMessage{Role: m.Role, Content: m.Content})
		}
	}
	removed := len(displayed) - len(displayTail)
	if removed < 0 {
		removed = 0
	}
	return append([]openai.ChatCompletionMessage{{
		Role:    "assistant",
		Content: compactedNotice(removed),
	}}, displayTail...)
}
