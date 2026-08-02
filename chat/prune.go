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

// prunedStub renders the placeholder that replaces a dropped tool result. It
// names the tool and the path so the model can tell WHAT it lost, and says how
// to get it back, so losing it is recoverable rather than merely confusing.
func prunedStub(name, path, detail string) string {
	switch {
	case path != "" && detail != "":
		return fmt.Sprintf("[%s %s — %s dropped to save context; re-read for current contents]", name, path, detail)
	case path != "":
		return fmt.Sprintf("[%s %s — output dropped to save context; re-read for current contents]", name, path)
	default:
		return fmt.Sprintf("[%s — output dropped to save context; re-read if needed]", name)
	}
}

// pruneToolOutputs returns a copy of msgs with eligible tool results replaced by
// stubs, the ids newly stubbed on this call, and the estimated tokens freed.
//
// already is the set of ids stubbed on previous calls. Passing it back in is
// what makes the policy MONOTONIC: a result never un-stubs, so the prompt
// prefix changes only at a prune rather than on every call. A rule that
// re-derived its boundary each time would invalidate the server's prefix cache
// on every request, and would very likely cost more than the tokens it saved.
//
// msgs is never written through — the caller's slice belongs to cogito, and
// behind it to the session's fragment, which pruning must not touch.
func pruneToolOutputs(msgs []openai.ChatCompletionMessage, cfg types.ToolOutputPruningConfig, already map[string]bool) ([]openai.ChatCompletionMessage, []string, int) {
	out := make([]openai.ChatCompletionMessage, len(msgs))
	copy(out, msgs)
	if cfg.Disabled {
		return out, nil, 0
	}

	calls := indexToolCalls(msgs)
	protected := trailingToolRun(msgs)

	// Everything that should end up stubbed on this call.
	target := make(map[string]bool, len(already))
	for id := range already {
		target[id] = true
	}
	if !cfg.DisableStaleReads {
		for id := range staleReadIDs(msgs, calls) {
			if !protected[id] {
				target[id] = true
			}
		}
	}
	if cfg.HighWaterTokens > 0 {
		for _, id := range sweepToLowWater(msgs, cfg, target, protected) {
			target[id] = true
		}
	}

	var newly []string
	freed := 0
	for i := range out {
		if out[i].Role != "tool" || !target[out[i].ToolCallID] {
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
		stub := prunedStub(info.name, info.path, "")
		if out[i].Content == stub {
			continue // already a stub in this copy
		}
		if !already[out[i].ToolCallID] {
			newly = append(newly, out[i].ToolCallID)
			freed += tokensOf(out[i].Content) - tokensOf(stub)
		}
		out[i].Content = stub
	}
	if freed < 0 {
		freed = 0
	}
	return out, newly, freed
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
func sweepToLowWater(msgs []openai.ChatCompletionMessage, cfg types.ToolOutputPruningConfig, target, protected map[string]bool) []string {
	total := 0
	for _, m := range msgs {
		if m.Role == "tool" && !target[m.ToolCallID] {
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
		if target[m.ToolCallID] || protected[m.ToolCallID] {
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
