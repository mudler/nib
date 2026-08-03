package chat

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/mudler/nib/types"
	openai "github.com/sashabaranov/go-openai"
)

// toolCallInfo is the call that produced a tool result: the tool's name, the
// path argument when it has one, and the index of the assistant message
// carrying the call.
//
// The index is what makes ordering questions answerable — "was this file edited
// AFTER this read" is the whole stale-read rule, and a tool result carries no
// position of its own beyond where it sits in the slice.
type toolCallInfo struct {
	name string
	path string
	idx  int
}

// indexToolCalls maps tool_call_id to the call that produced it.
//
// A tool result is a role:"tool" message carrying only a ToolCallID and content;
// the tool's name and arguments live in an earlier assistant message. Any rule
// that cares what a result came FROM has to walk that correlation first.
func indexToolCalls(msgs []openai.ChatCompletionMessage) map[string]toolCallInfo {
	out := make(map[string]toolCallInfo)
	for i, m := range msgs {
		for _, tc := range m.ToolCalls {
			info := toolCallInfo{name: tc.Function.Name, idx: i}
			// Arguments are model-generated JSON. Unparseable arguments are a
			// fact of life with weaker models, and they must not panic or
			// poison the index: the call keeps its name and simply has no path,
			// so no path-scoped rule will match it.
			var args struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err == nil {
				info.path = args.Path
			}
			out[tc.ID] = info
		}
	}
	return out
}

// invalidatingTools are the tools whose success means any earlier read of the
// same path no longer matches what is on disk.
var invalidatingTools = map[string]bool{"edit": true, "write": true}

// staleReadIDs returns the tool_call_ids of read results a later edit or write
// invalidated.
//
// This rule is about correctness before tokens. Once a file has been edited, an
// earlier read of it is not merely large, it is WRONG — and a model reasoning
// from it will make decisions about content that no longer exists. Dropping it
// costs a prefix-cache re-prefill exactly at the moment the content stopped
// being trustworthy, which is the right moment to pay.
func staleReadIDs(msgs []openai.ChatCompletionMessage, calls map[string]toolCallInfo) map[string]bool {
	// Last index at which each path was modified.
	//
	// The seen check is deliberate, and it is NOT dead weight even though no
	// test can currently fail without it. A missing map entry reads back as 0,
	// indistinguishable from a real modification at index 0, so the plainer
	// `info.idx > modifiedAt[p]` silently declines to record an edit in the
	// very first message. That happens to be unobservable while idx comes from
	// indexToolCalls: staleness below requires at > info.idx with info.idx >= 0,
	// so a modification at index 0 can never invalidate anything anyway. The
	// check is what stops that accident from becoming load-bearing the moment a
	// caller supplies indices on any other basis.
	modifiedAt := make(map[string]int)
	for _, info := range calls {
		if !invalidatingTools[info.name] || info.path == "" {
			continue
		}
		p := filepath.Clean(info.path)
		if at, seen := modifiedAt[p]; !seen || info.idx > at {
			modifiedAt[p] = info.idx
		}
	}
	if len(modifiedAt) == 0 {
		return map[string]bool{}
	}

	stale := make(map[string]bool)
	for _, m := range msgs {
		if m.Role != "tool" || m.ToolCallID == "" {
			continue
		}
		info, ok := calls[m.ToolCallID]
		if !ok || info.name != "read" || info.path == "" {
			continue
		}
		// Strictly after: tool calls issued in the SAME assistant message run
		// in parallel with no defined order between them, so a read alongside
		// an edit cannot be shown to be looking at pre-edit content.
		if at, ok := modifiedAt[filepath.Clean(info.path)]; ok && at > info.idx {
			stale[m.ToolCallID] = true
		}
	}
	return stale
}

// The clauses a stub can carry in place of the body it replaced.
//
// The stub is the ONLY channel telling the model why content it can see it once
// had is gone, and the two reasons ask for different behaviour. A body dropped
// for budget was still accurate when it went, so a model that remembers it is
// not wrong. A body dropped because a later edit invalidated it is the opposite
// case: remembering it is precisely the failure the rule exists to prevent, and
// "we evicted this to save context" invites exactly that.
//
// Both are fixed phrases carrying no figures. The stub's text must be a
// function of the reason alone, so that recording the reason once is enough to
// reproduce the same text byte for byte on every later call — see
// pruneToolOutputs on why that matters.
const (
	detailBudget = "output dropped to save context"
	detailStale  = "output dropped after a later edit"
)

// prunedStub renders the placeholder that replaces a dropped tool result. It
// names the tool and the path so the model can tell WHAT it lost, gives the
// reason it went, and says how to get it back, so losing it is recoverable
// rather than merely confusing.
//
// An empty detail renders the budget wording: a caller with no reason to give
// still produces a complete sentence rather than a gap.
func prunedStub(name, path, detail string) string {
	if detail == "" {
		detail = detailBudget
	}
	if path != "" {
		return fmt.Sprintf("[%s %s — %s; re-read for current contents]", name, path, detail)
	}
	return fmt.Sprintf("[%s — %s; re-read if needed]", name, detail)
}

// stubbedResult is one tool result this call newly replaced with a stub: its id
// and the clause its stub carries.
//
// The clause travels with the id because the caller has to STORE it, not merely
// display it. Why a result was dropped is not re-derivable later: a result
// swept for budget becomes a stale read the moment the model edits the file it
// had read, and re-deriving the clause would then rewrite a stub already
// sitting in the prompt. Recording the reason at the transition and keeping it
// is what makes the stub's text immutable.
type stubbedResult struct {
	id     string
	detail string
}

// pruneToolOutputs returns a copy of msgs with eligible tool results replaced by
// stubs, the ids newly stubbed on this call, and the estimated tokens freed.
//
// already maps each id stubbed on previous calls to the clause its stub
// carries. Passing it back in is what makes the policy MONOTONIC: a result
// never un-stubs, so the prompt prefix changes only at a prune rather than on
// every call. A rule that re-derived its boundary each time would invalidate
// the server's prefix cache on every request, and would very likely cost more
// than the tokens it saved.
//
// It carries the clause rather than a bare membership flag because monotonicity
// is a property of the TEXT, not only of the set. Rewriting a stub's wording
// moves the prefix exactly as un-stubbing would, so an id keeps the clause it
// was first stubbed with even when the reason it would be picked for today has
// changed.
//
// Disabled is the ONE deliberate exception to that monotonicity, and it is
// worth stating because everything else here is built to make un-stubbing
// impossible. Returning before the stubbing loop hands back the untouched
// bodies, so a mid-session Reload that turns pruning off un-stubs the whole
// conversation in one step. That is the point: switching the feature off must
// actually switch it off, not leave a session serving stubs for content it
// still holds. It is safe because it costs only what any prefix change costs —
// one re-prefill — and it cannot lose anything, since the bodies come back from
// the caller's own slice rather than from anything pruning stored. `already`
// deliberately survives the early return, so re-enabling restores exactly the
// stubs that were there before, with their original clauses, rather than
// re-deriving a fresh boundary.
//
// msgs is never written through — the caller's slice belongs to cogito, and
// behind it to the session's fragment, which pruning must not touch.
func pruneToolOutputs(msgs []openai.ChatCompletionMessage, cfg types.ToolOutputPruningConfig, already map[string]string) ([]openai.ChatCompletionMessage, []stubbedResult, int) {
	out := make([]openai.ChatCompletionMessage, len(msgs))
	copy(out, msgs)
	if cfg.Disabled {
		return out, nil, 0
	}

	calls := indexToolCalls(msgs)
	protected := trailingToolRun(msgs)

	// Everything that should end up stubbed on this call, mapped to the clause
	// its stub renders.
	target := make(map[string]string, len(already))
	for id, detail := range already {
		target[id] = detail
	}
	if !cfg.DisableStaleReads {
		for id := range staleReadIDs(msgs, calls) {
			// An id already in target keeps the clause it was first stubbed
			// with; only a result being stubbed for the first time gets today's
			// reason.
			if _, done := target[id]; done || protected[id] {
				continue
			}
			target[id] = detailStale
		}
	}
	if cfg.HighWaterTokens > 0 {
		// sweepToLowWater passes over everything already in target, so this
		// never overwrites a stale read's clause with the budget one.
		for _, id := range sweepToLowWater(msgs, cfg, target, protected) {
			target[id] = detailBudget
		}
	}

	var newly []stubbedResult
	freed := 0
	for i := range out {
		if out[i].Role != "tool" {
			continue
		}
		detail, ok := target[out[i].ToolCallID]
		if !ok {
			continue
		}
		info := calls[out[i].ToolCallID]
		// Compaction can drop the assistant message that issued a call while its
		// result survives, leaving no name to render. The sweep has no reason to
		// skip such a result — it is large like any other — but the stub is text
		// the model reads, so it must still name something.
		if info.name == "" {
			info.name = "tool"
		}
		stub := prunedStub(info.name, info.path, detail)
		if out[i].Content == stub {
			continue // already a stub in this copy
		}
		if _, seen := already[out[i].ToolCallID]; !seen {
			newly = append(newly, stubbedResult{id: out[i].ToolCallID, detail: detail})
			freed += tokensOf(out[i].Content) - tokensOf(stub)
		}
		out[i].Content = stub
	}
	if freed < 0 {
		freed = 0
	}
	return out, newly, freed
}

// pruneMessages is the cogito.WithMessagesManipulator body: it rewrites the
// messages about to be sent, and records what it stubbed so the next call makes
// the same decisions.
//
// It runs on the turn goroutine but the state is guarded anyway — the
// manipulator is called from inside cogito's loop, and nothing here should
// assume which goroutine that is.
func (s *Session) pruneMessages(msgs []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	s.prunedMu.Lock()
	if s.prunedIDs == nil {
		s.prunedIDs = map[string]string{}
	}
	out, newly, freed := pruneToolOutputs(msgs, effectivePruning(s.pruning), s.prunedIDs)
	for _, n := range newly {
		s.prunedIDs[n.id] = n.detail
	}
	forgetAbsentIDs(s.prunedIDs, msgs)
	s.prunedMu.Unlock()

	// Fired outside the lock, like OnCompactDone: this is host UI code running
	// on nib's goroutine, and a callback that reaches back into the session must
	// not be able to deadlock the turn.
	if len(newly) > 0 && s.callbacks.OnPruneDone != nil {
		s.callbacks.OnPruneDone(len(newly), freed)
	}
	return out
}

// effectivePruning returns cfg with a low-water mark that cannot exceed the high
// -water mark.
//
// A config with low above high is not a stricter policy, it is an inert one: the
// sweep starts when the total reaches the high mark and then immediately finds
// itself already under the low mark, so crossing the mark prunes nothing at all
// — and keeps crossing it on every subsequent call. Clamping is applied here,
// where a config becomes a live policy, rather than in config defaulting,
// because NewSession takes a types.Config from embedders too and never sees
// config.Load's defaulting.
func effectivePruning(cfg types.ToolOutputPruningConfig) types.ToolOutputPruningConfig {
	if cfg.LowWaterTokens > cfg.HighWaterTokens {
		cfg.LowWaterTokens = cfg.HighWaterTokens
	}
	return cfg
}

// forgetAbsentIDs drops stubbed ids whose tool result is no longer in the
// conversation.
//
// Without this the set only ever grows: compaction rewrites history and removes
// whole exchanges, and their ids would then sit in the map for the life of the
// session, describing messages that no longer exist. Forgetting them cannot
// un-stub anything, because a result that is gone cannot come back — cogito
// hands the manipulator the whole fragment on every call, and the sub-agent
// option set deliberately does not carry the manipulator, so a short sub-agent
// conversation can never be mistaken for a shrunken main one.
//
// That "whole fragment" holds only while nib leaves cogito's autoPlan,
// auto-improve and reviewer paths off. Each of them (plan.go:259, plan.go:470,
// reviewer.go:29, autoimprove.go:121) forwards the caller's opts — the
// manipulator among them — to ExecuteTools over a DERIVED sub-fragment. nib sets
// none of those options today; enabling one would run this function against a
// subtask fragment, flush the stubbed set, and un-stub everything on the way
// back to the main conversation, breaking monotonicity. Gate the manipulator on
// the main fragment before turning any of them on.
func forgetAbsentIDs(ids map[string]string, msgs []openai.ChatCompletionMessage) {
	if len(ids) == 0 {
		return
	}
	present := make(map[string]bool, len(msgs))
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID != "" {
			present[m.ToolCallID] = true
		}
	}
	for id := range ids {
		if !present[id] {
			delete(ids, id)
		}
	}
}

// tokensOf is the byte/4 estimate compaction already uses, applied to one body.
func tokensOf(s string) int { return len(s) / 4 }

// trailingToolRun returns the ids of the contiguous run of tool results at the
// END of msgs. Those are the results the model is about to reason over, so they
// are never stubbed.
//
// This does not break monotonicity: as the conversation grows they stop being
// trailing and become eligible, which is a kept->stubbed transition — the only
// direction the policy ever moves.
func trailingToolRun(msgs []openai.ChatCompletionMessage) map[string]bool {
	protected := map[string]bool{}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "tool" {
			break
		}
		if msgs[i].ToolCallID != "" {
			protected[msgs[i].ToolCallID] = true
		}
	}
	return protected
}

// sweepToLowWater picks the oldest eligible results to stub until the total
// tool-output size drops below cfg.LowWaterTokens, returning their ids in
// oldest-first order.
//
// It stops when nothing eligible is left rather than violating MinResultTokens:
// size pruning is best-effort and never guarantees a ceiling.
func sweepToLowWater(msgs []openai.ChatCompletionMessage, cfg types.ToolOutputPruningConfig, target map[string]string, protected map[string]bool) []string {
	total := 0
	for _, m := range msgs {
		if m.Role != "tool" {
			continue
		}
		if _, done := target[m.ToolCallID]; !done {
			total += tokensOf(m.Content)
		}
	}
	if total < cfg.HighWaterTokens {
		return nil
	}

	var picked []string
	for _, m := range msgs {
		if total < cfg.LowWaterTokens {
			break
		}
		if m.Role != "tool" || m.ToolCallID == "" {
			continue
		}
		if _, done := target[m.ToolCallID]; done || protected[m.ToolCallID] {
			continue
		}
		size := tokensOf(m.Content)
		if size < cfg.MinResultTokens {
			continue
		}
		picked = append(picked, m.ToolCallID)
		total -= size
	}
	return picked
}
