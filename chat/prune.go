package chat

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mudler/nib/codeindex"
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
//
// offset and limit are the read tool's line range. They are zero when the call
// has none, which the read tool takes as "from the start" and "to the end".
type toolCallInfo struct {
	name   string
	path   string
	idx    int
	offset int
	limit  int
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
				Path   string `json:"path"`
				Offset int    `json:"offset"`
				Limit  int    `json:"limit"`
			}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err == nil {
				info.path, info.offset, info.limit = args.Path, args.Offset, args.Limit
			}
			out[tc.ID] = info
		}
	}
	return out
}

// invalidatingTools are the tools whose success means any earlier read of the
// same path no longer matches what is on disk.
var invalidatingTools = map[string]bool{"edit": true, "write": true}

// pathObservingTools are the tools whose result describes a file as it was when
// the call ran, so an edit or write of that file makes the result wrong. An
// index result carries line ranges, and an edit shifts them.
var pathObservingTools = map[string]bool{"read": true, "index": true}

// staleReadIDs returns the tool_call_ids of read and index results a later edit
// or write invalidated.
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
		if !ok || !pathObservingTools[info.name] || info.path == "" {
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

// supersededReadIDs returns the tool_call_ids of read results that a later read
// of the same file covers.
//
// Dropping such a result loses nothing: the later read holds the same lines, and
// it holds them as they are now. That makes them the first choice when the size
// sweep must free space. Without that choice the sweep drops the oldest reads,
// which are often other files the model still needs, and the model re-reads
// them.
//
// A later read covers an earlier one only when its line range contains the
// earlier range. As in staleReadIDs, the later call must be in a later assistant
// message.
//
// A read that returned the file's outline covers nothing: it has no range, but
// it holds none of the file's lines (see isOutlineRead).
func supersededReadIDs(msgs []openai.ChatCompletionMessage, calls map[string]toolCallInfo) map[string]bool {
	outlines := make(map[string]bool)
	for _, m := range msgs {
		if m.Role == "tool" && isOutlineRead(m.Content) {
			outlines[m.ToolCallID] = true
		}
	}
	byPath := make(map[string][]toolCallInfo)
	for id, info := range calls {
		if info.name == "read" && info.path != "" && !outlines[id] {
			p := filepath.Clean(info.path)
			byPath[p] = append(byPath[p], info)
		}
	}

	out := make(map[string]bool)
	for _, m := range msgs {
		if m.Role != "tool" || m.ToolCallID == "" {
			continue
		}
		info, ok := calls[m.ToolCallID]
		if !ok || info.name != "read" || info.path == "" {
			continue
		}
		for _, later := range byPath[filepath.Clean(info.path)] {
			if later.idx > info.idx && readCovers(later, info) {
				out[m.ToolCallID] = true
				break
			}
		}
	}
	return out
}

// isOutlineRead reports whether a read result is the outline the read tool
// returns in place of a large source file (mcp/filesystem.go outlineRead). The
// result arrives as the MCP JSON envelope of readFileOutput, so a body that is
// not JSON is never an outline.
func isOutlineRead(content string) bool {
	if !strings.Contains(content, `"outline"`) {
		return false
	}
	var r struct {
		Outline bool `json:"outline"`
	}
	return json.Unmarshal([]byte(content), &r) == nil && r.Outline
}

// readCovers reports whether read a returns every line that read b returns.
// A zero or negative limit means "to the end of the file".
func readCovers(a, b toolCallInfo) bool {
	if a.offset > b.offset {
		return false
	}
	if a.limit <= 0 {
		return true
	}
	if b.limit <= 0 {
		return false
	}
	return a.offset+a.limit >= b.offset+b.limit
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
//
// A superseded read is the third case. Its content is still in the prompt, in
// a later read of the same file, so its stub must NOT ask for a re-read. That
// request would start the loop that dropping the result was meant to stop.
const (
	detailBudget     = "output dropped to save context"
	detailStale      = "output dropped after a later edit"
	detailSuperseded = "output dropped, a later read of the same file has it"
)

// prunedStub renders the placeholder that replaces a dropped tool result. It
// names the tool and the path so the model can tell WHAT it lost, gives the
// reason it went, and says how to get it back, so losing it is recoverable
// rather than merely confusing.
//
// An empty detail renders the budget wording: a caller with no reason to give
// still produces a complete sentence rather than a gap.
//
// A clause can carry the file's outline after an outlineSep (see
// attachOutlines). The outline follows the bracketed line, so the stub is still
// a function of the stored clause alone.
func prunedStub(name, path, detail string) string {
	detail, outline, _ := strings.Cut(detail, outlineSep)
	if detail == "" {
		detail = detailBudget
	}
	var stub string
	switch {
	case detail == detailSuperseded:
		stub = fmt.Sprintf("[%s %s — %s]", name, path, detail)
	case path != "":
		stub = fmt.Sprintf("[%s %s — %s; re-read for current contents]", name, path, detail)
	default:
		stub = fmt.Sprintf("[%s — %s; re-read if needed]", name, detail)
	}
	if outline != "" {
		stub += "\nOutline when dropped (line ranges in []; read one part back with offset and limit):\n" + outline
	}
	return stub
}

// outlineSep separates a stub clause from the outline stored with it. The
// clauses are fixed phrases with no newline, so the first newline is the split.
const outlineSep = "\n"

// maxStubOutlineBytes caps the outline a stub carries. A stub exists to free
// space, so an outline of a very large file is cut rather than kept whole.
const maxStubOutlineBytes = 4096

// attachOutlines adds the file's outline to the stubs of newly dropped reads,
// and returns the tokens the outlines add back.
//
// Without it a dropped read leaves only "re-read for current contents", and the
// model reads the whole file again to recover what it held. With the outline,
// the model still knows what the file contains and where, and can read back
// only the part it needs.
//
// It rewrites both the stub in out and the clause in newly, which the caller
// stores. The outline is computed once, at the transition, and after that is
// part of the stored clause: re-computing it from disk on every call would
// change the stub whenever the file changed and move the prompt prefix.
//
// Only budget and stale reads get an outline. A superseded read's lines are in
// a later read already. A read that returned an outline has nothing more to
// give, and an outline no smaller than the result it replaces frees nothing.
// outlineOf returns "" when the file cannot be indexed.
func attachOutlines(msgs, out []openai.ChatCompletionMessage, newly []stubbedResult, outlineOf func(path string) string) int {
	if len(newly) == 0 {
		return 0
	}
	calls := indexToolCalls(msgs)
	pos := make(map[string]int, len(msgs))
	for i, m := range msgs {
		if m.Role == "tool" && m.ToolCallID != "" {
			pos[m.ToolCallID] = i
		}
	}
	added := 0
	for k, n := range newly {
		info := calls[n.id]
		i, ok := pos[n.id]
		if !ok || info.name != "read" || info.path == "" ||
			(n.detail != detailBudget && n.detail != detailStale) ||
			isOutlineRead(msgs[i].Content) {
			continue
		}
		outline := capOutline(outlineOf(info.path))
		if outline == "" {
			continue
		}
		detail := n.detail + outlineSep + outline
		stub := prunedStub(info.name, info.path, detail)
		if len(stub) >= len(msgs[i].Content) {
			continue
		}
		added += tokensOf(stub) - tokensOf(out[i].Content)
		out[i].Content = stub
		newly[k].detail = detail
	}
	return added
}

// capOutline cuts an outline to maxStubOutlineBytes at a line boundary.
func capOutline(outline string) string {
	outline = strings.TrimRight(outline, "\n")
	if len(outline) <= maxStubOutlineBytes {
		return outline
	}
	cut := strings.LastIndexByte(outline[:maxStubOutlineBytes], '\n')
	if cut < 0 {
		cut = maxStubOutlineBytes
	}
	return outline[:cut] + "\n... (outline cut; index the file for the rest)"
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
		for _, r := range sweepToLowWater(msgs, cfg, target, protected, supersededReadIDs(msgs, calls)) {
			target[r.id] = r.detail
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

// pruneMessages rewrites the messages about to be sent, and records what it
// stubbed so the next call makes the same decisions. The request path reaches
// it through progressivePrune, which compresses what it leaves in full.
//
// It runs on the turn goroutine but the state is guarded anyway — the
// manipulator is called from inside cogito's loop, and nothing here should
// assume which goroutine that is.
func (s *Session) pruneMessages(msgs []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	// Read the window and threshold outside the prunedMu lock: both take
	// modelMu, which must not nest under prunedMu.
	window := s.contextWindow()
	threshold := s.compactionConfig().Threshold

	s.prunedMu.Lock()
	if s.prunedIDs == nil {
		s.prunedIDs = map[string]string{}
	}
	promptTokens := prunedViewTokens(msgs, s.prunedIDs)
	out, newly, freed := pruneToolOutputs(msgs, effectivePruning(s.pruning, promptTokens, window, threshold), s.prunedIDs)
	freed -= attachOutlines(msgs, out, newly, s.stubOutline)
	freed = max(freed, 0)
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

// stubOutline returns the codeindex outline of a file a dropped read named, or
// "" when the file cannot be indexed.
func (s *Session) stubOutline(path string) string {
	outline, err := codeindex.Index(resolveWorkspacePath(s.workingDir, path))
	if err != nil {
		return ""
	}
	return outline
}

// prunedViewTokens estimates the prompt that is actually sent: msgs with every
// result in already replaced by its stub.
//
// Pressure scaling must measure this view, not the raw msgs. The raw fragment
// still holds the full body of every stubbed result, so its size only grows. On
// a long session it passes the compaction threshold while the prompt sent is a
// small part of the window. Compaction measures the real prompt and does not
// fire, so nothing resets the fragment. The marks then stay at their floor for
// the rest of the session. The sweep keeps only the one or two latest reads, and
// the model re-reads the files it lost in a loop. Each re-read makes the raw
// count larger.
func prunedViewTokens(msgs []openai.ChatCompletionMessage, already map[string]string) int {
	n := estimateTokens(msgs)
	if len(already) == 0 {
		return n
	}
	calls := indexToolCalls(msgs)
	for _, m := range msgs {
		detail, ok := already[m.ToolCallID]
		if m.Role != "tool" || !ok {
			continue
		}
		info := calls[m.ToolCallID]
		if info.name == "" {
			info.name = "tool"
		}
		n -= tokensOf(m.Content) - tokensOf(prunedStub(info.name, info.path, detail))
	}
	return max(n, 0)
}

// pruningMinScale is the floor for pressure-scaled water marks. At the
// compaction threshold (default 80% utilization), water marks are scaled down
// to this fraction of their configured value. This makes pruning most
// aggressive right before compaction would fire, reclaiming every token the
// sweep can so the summary has less to cover.
const pruningMinScale = 0.25

// pruningHighWaterFraction and pruningLowWaterFraction set the water marks
// relative to the context window. The configured marks are absolute token
// counts, and on their own they are tuned for small windows: 24k of tool
// output is three medium file reads, which on a 200k or 1M window stubbed
// results the model had only just fetched. The effective mark is the larger
// of the configured value and this fraction of the window, so a small window
// keeps its configured policy and a large one gets room in proportion to it.
//
// The low mark is 20% and not 10% because a sweep cuts all the way down to it.
// At 10% of a 200k window, a sweep kept three 6k reads, less than a normal
// working set, and the model re-read the rest in a loop.
const (
	pruningHighWaterFraction = 0.30
	pruningLowWaterFraction  = 0.20
)

// effectivePruning returns cfg with water marks sized to the context window,
// scaled by context utilization, and a low-water mark that cannot exceed the
// high-water mark.
//
// Window sizing: each configured mark is a floor, raised to
// pruningHighWaterFraction / pruningLowWaterFraction of the window when that
// is larger. This runs before pressure scaling, so the scaling applies to the
// window-sized marks.
//
// Pressure scaling: the window-sized marks are the values at zero
// utilization. As the context fills, both marks shrink linearly toward
// pruningMinScale × that value, reaching that floor at the compaction
// threshold. Below the threshold, pruning is gentler (larger marks → sweeps
// less often and less deeply); at the threshold, it is at its most aggressive
// because compaction is about to fire anyway and every reclaimed token is one
// the summary does not have to cover.
//
// Both steps are skipped when size pruning is off (HighWaterTokens ≤ 0) or the
// window is unknown, and pressure scaling is skipped when there is nothing to
// measure — the stale-read rule
// still applies in all those cases.
//
// The low ≤ high clamp is applied here, where a config becomes a live policy,
// rather than in config defaulting, because NewSession takes a types.Config
// from embedders too and never sees config.Load's defaulting. The clamp runs
// both before scaling (to fix a misconfigured policy) and after (to fix integer
// truncation that can flip the invariant by 1).
func effectivePruning(cfg types.ToolOutputPruningConfig, promptTokens, window int, threshold float64) types.ToolOutputPruningConfig {
	if cfg.LowWaterTokens > cfg.HighWaterTokens {
		cfg.LowWaterTokens = cfg.HighWaterTokens
	}
	if cfg.HighWaterTokens <= 0 || window <= 0 {
		return cfg
	}
	if high := int(float64(window) * pruningHighWaterFraction); high > cfg.HighWaterTokens {
		cfg.HighWaterTokens = high
	}
	if low := int(float64(window) * pruningLowWaterFraction); low > cfg.LowWaterTokens {
		cfg.LowWaterTokens = low
	}
	if promptTokens <= 0 {
		return cfg
	}
	if threshold <= 0 || threshold > 1 {
		threshold = 0.8
	}
	utilization := float64(promptTokens) / float64(window)
	if utilization > threshold {
		utilization = threshold
	}
	scale := 1.0 - (utilization/threshold)*(1.0-pruningMinScale)
	cfg.HighWaterTokens = int(float64(cfg.HighWaterTokens) * scale)
	cfg.LowWaterTokens = int(float64(cfg.LowWaterTokens) * scale)
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

// trailingTailBudget is the maximum bytes of tool output that trailingToolRun
// protects from stubbing. Results beyond this budget — measured from the most
// recent — become eligible for size pruning, so a single huge output cannot
// monopolize the protected tail.
const trailingTailBudget = 64 * 1024

// trailingToolRun returns the ids of the contiguous run of tool results at the
// END of msgs, up to trailingTailBudget bytes. Those are the results the model
// is about to reason over, so they are never stubbed — but only up to the
// budget, so that a multi-megabyte output does not make the entire trailing run
// untouchable.
//
// The most recent result is always protected regardless of size: the model
// just received it and needs it to reason. The budget prevents additional
// trailing results from stacking up behind it.
//
// This does not break monotonicity: as the conversation grows they stop being
// trailing and become eligible, which is a kept->stubbed transition — the only
// direction the policy ever moves.
func trailingToolRun(msgs []openai.ChatCompletionMessage) map[string]bool {
	protected := map[string]bool{}
	var size int
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "tool" {
			break
		}
		if msgs[i].ToolCallID == "" {
			continue
		}
		if size >= trailingTailBudget {
			break
		}
		size += len(msgs[i].Content)
		protected[msgs[i].ToolCallID] = true
	}
	return protected
}

// sweepToLowWater picks eligible results to stub until the total tool-output
// size drops below cfg.LowWaterTokens, and returns each one with the clause its
// stub carries.
//
// It takes the superseded reads first, oldest first, because dropping them
// loses nothing. Then it takes the remaining results, oldest first.
//
// Superseded reads are stubbed only here, in a sweep, and not as soon as the
// later read arrives. Stubbing them at once would change an early message on
// every re-read and cost a prefix-cache re-prefill each time. The stale-read
// rule pays that cost because the old content is wrong. A superseded read is
// only redundant.
//
// It stops when nothing eligible is left rather than violating MinResultTokens:
// size pruning is best-effort and never guarantees a ceiling.
func sweepToLowWater(msgs []openai.ChatCompletionMessage, cfg types.ToolOutputPruningConfig, target map[string]string, protected, superseded map[string]bool) []stubbedResult {
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

	var picked []stubbedResult
	taken := make(map[string]bool)
	pass := func(onlySuperseded bool) {
		for _, m := range msgs {
			if total < cfg.LowWaterTokens {
				return
			}
			if m.Role != "tool" || m.ToolCallID == "" || taken[m.ToolCallID] {
				continue
			}
			if onlySuperseded && !superseded[m.ToolCallID] {
				continue
			}
			if _, done := target[m.ToolCallID]; done || protected[m.ToolCallID] {
				continue
			}
			size := tokensOf(m.Content)
			if size < cfg.MinResultTokens {
				continue
			}
			detail := detailBudget
			if superseded[m.ToolCallID] {
				detail = detailSuperseded
			}
			picked = append(picked, stubbedResult{id: m.ToolCallID, detail: detail})
			taken[m.ToolCallID] = true
			total -= size
		}
	}
	pass(true)
	pass(false)
	return picked
}
