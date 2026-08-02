package chat

import (
	"encoding/json"
	"fmt"
	"path/filepath"

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
