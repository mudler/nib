package chat

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
	openai "github.com/sashabaranov/go-openai"
)

// trimStep records one step of the iterativeTrim escalation chain and whether
// it was the one that made the history fit.
type trimStep struct {
	name    string
	applied bool
}

// truncatedMarker is the text of the user-role message that stands in for the
// history a hard truncation drops.
const truncatedMarker = "Previous conversation truncated to fit context window."

// iterativeTrim makes the fragment fit the context budget after an overflow.
// It runs an escalation chain and validates each step's result with
// validateCompaction before it moves on:
//
//  1. LLM compaction (compactHistory).
//  2. Compaction again with a halved KeepRecent, down to 1, when step 1
//     succeeded but its result did not fit (a large verbatim tail). It is
//     skipped when the summary itself failed: a smaller keep makes the head
//     larger, and summarizeFitting already moves the boundary for that case.
//  3. Aggressive prune: every tool output outside the trailing run is
//     replaced by its stub, and the stubs are committed to the fragment.
//  4. Hard truncate: the history is saved as an artifact, and everything
//     before a short tail is replaced by a user-role marker naming it.
//
// It returns nil when a step made the fragment fit, and the last validation
// error (errors.Is works on the Task 1 sentinels) when none did.
func (s *Session) iterativeTrim(ctx context.Context) error {
	cfg := s.compactionConfig()
	budget := s.trimBudget(cfg)
	steps := []trimStep{{name: "compact"}, {name: "shrink-keep"}, {name: "prune"}, {name: "truncate"}}
	done := func(i int) error {
		steps[i].applied = true
		xlog.Info("overflow recovery trimmed the history", "step", steps[i].name, "steps", steps)
		return nil
	}

	// Step 1: LLM compaction.
	before := s.fragmentSnapshot()
	_, _, cerr := s.compactHistory(ctx)
	lastErr := cerr
	if cerr == nil {
		lastErr = s.checkCompaction(before, budget)
		if lastErr == nil {
			return done(0)
		}
		// Step 2: the summary worked but the result does not fit. Retry with
		// a smaller tail, without touching the shared config.
		for keep := max(cfg.KeepRecent/2, 1); ; keep /= 2 {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			before = s.fragmentSnapshot()
			if _, _, err := s.compactHistoryKeep(ctx, keep); err != nil {
				lastErr = err
				break
			}
			lastErr = s.checkCompaction(before, budget)
			if lastErr == nil {
				return done(1)
			}
			if keep <= 1 {
				break
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Step 3: stub every tool output the prune may drop.
	if err := s.forcePrune(budget); err == nil {
		return done(2)
	} else {
		lastErr = err
	}

	// Step 4: hard truncate.
	if err := s.hardTruncate(cfg, budget); err == nil {
		return done(3)
	} else {
		lastErr = err
	}
	xlog.Warn("overflow recovery could not fit the history", "error", lastErr)
	return fmt.Errorf("iterative trim: %w", lastErr)
}

// trimBudget is the size the trimmed history must fit: the context budget
// less the prompt the tool schemas and system prompt take. With no known
// window there is nothing to measure against, and any shrink passes.
func (s *Session) trimBudget(cfg types.CompactionConfig) int {
	window := s.contextWindow()
	if window <= 0 {
		return math.MaxInt
	}
	budget := ContextBudget(cfg, window)
	if floor := s.SchemaBudget().Floor; floor > 0 {
		budget -= floor
	}
	return budget
}

// fragmentState is what a step needs to put back when its result is invalid.
type fragmentState struct {
	frag     cogito.Fragment
	messages []openai.ChatCompletionMessage
}

func (s *Session) fragmentSnapshot() fragmentState {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	return fragmentState{frag: s.fragment, messages: s.messages}
}

// checkCompaction validates the fragment a compaction installed against the
// one before it. A result that is smaller but over budget stays in place, as
// the next step builds on it. A result that grew, came out empty or broke the
// tool pairing is worse than what it replaced, so the old state goes back.
func (s *Session) checkCompaction(before fragmentState, budget int) error {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	err := validateCompaction(before.frag.Messages, s.fragment.Messages, budget)
	if err != nil && !errors.Is(err, ErrCompactionOverBudget) && !errors.Is(err, ErrCompactionNoOp) {
		s.fragment = before.frag
		s.messages = before.messages
	}
	return err
}

// installFragment replaces the fragment with msgs, and the display copy with
// display when it is not nil. Like compactHistory, it keeps the running token
// counters but not LastUsage, which measured the history being replaced.
func (s *Session) installFragment(msgs, display []openai.ChatCompletionMessage) {
	s.historyMu.Lock()
	newFrag := cogito.NewFragment(msgs...)
	if s.fragment.Status != nil {
		statusCopy := *s.fragment.Status
		statusCopy.LastUsage = cogito.LLMUsage{}
		newFrag.Status = &statusCopy
	}
	s.fragment = newFrag
	if display != nil {
		s.messages = display
	}
	s.historyMu.Unlock()
	s.live.reset()
}

// forcePrune stubs every tool output except the trailing run and results
// below MinResultTokens, and commits the stubs to the fragment when the result
// fits the budget.
//
// It calls pruneToolOutputs directly rather than pruneMessages: a
// HighWaterTokens of 0 turns size pruning OFF, and effectivePruning raises
// the marks to a fraction of the window. A high mark of 1 and a low mark of 0
// make the sweep take everything eligible. The new stubs are recorded in
// prunedIDs, so the next request's prune renders the same bytes instead of
// rewriting them. Outlines are not attached: a later prune would replace a stub
// with an outline by the plain stub, which changes an already-sent message.
func (s *Session) forcePrune(budget int) error {
	before := s.fragmentSnapshot().frag.Messages
	s.prunedMu.Lock()
	if s.prunedIDs == nil {
		s.prunedIDs = map[string]string{}
	}
	forced := types.ToolOutputPruningConfig{
		HighWaterTokens: 1,
		LowWaterTokens:  0,
		MinResultTokens: s.pruning.MinResultTokens,
	}
	out, newly, _ := pruneToolOutputs(before, forced, s.prunedIDs)
	// Measure what the requests send: a result the prune keeps may already
	// be compressed to a recorded level (see progressivePrune). The new stubs
	// take precedence over a level, as they do in the requests.
	pruned := make(map[string]string, len(s.prunedIDs)+len(newly))
	for k, v := range s.prunedIDs {
		pruned[k] = v
	}
	for _, n := range newly {
		pruned[n.id] = n.detail
	}
	beforeView := requestView(before, s.prunedIDs, s.compressed)
	outView := requestView(out, pruned, s.compressed)
	s.prunedMu.Unlock()

	if err := validateCompaction(beforeView, outView, budget); err != nil {
		return err
	}
	s.prunedMu.Lock()
	for _, n := range newly {
		s.prunedIDs[n.id] = n.detail
	}
	s.prunedMu.Unlock()
	s.installFragment(out, nil)
	return nil
}

// hardTruncate keeps the shortest pairing-safe tail that fits, starting from
// KeepRecent messages and halving down to 1, and replaces everything before it
// with a user-role marker. A mid-conversation system message is rejected or
// moved by some adapters, and renderMessages drops it from later summaries, so
// the marker is a user message like summaryMessage. The whole history is
// saved as an artifact first and the marker names it.
func (s *Session) hardTruncate(cfg types.CompactionConfig, budget int) error {
	state := s.fragmentSnapshot()
	msgs := state.frag.Messages
	if len(msgs) < 2 {
		return fmt.Errorf("%w: nothing to truncate", ErrCompactionNoOp)
	}

	marker := truncatedMarker
	if uri := s.spillCompactionHead(renderMessages(msgs)); uri != "" {
		marker += " Use " + uri + " to page through the full history."
	}

	var lastErr error = fmt.Errorf("%w: nothing to truncate", ErrCompactionNoOp)
	for keep := max(cfg.KeepRecent, 1); ; keep /= 2 {
		head, tail := splitForCompaction(msgs, keep)
		if len(head) > 0 {
			candidate := append([]openai.ChatCompletionMessage{{Role: "user", Content: marker}}, tail...)
			lastErr = validateCompaction(msgs, candidate, budget)
			if lastErr == nil {
				s.installFragment(candidate, compactedDisplay(state.messages, tail))
				return nil
			}
		}
		if keep <= 1 {
			return lastErr
		}
	}
}
