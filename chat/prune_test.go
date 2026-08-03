package chat

import (
	"strings"
	"testing"

	"github.com/mudler/nib/types"
	openai "github.com/sashabaranov/go-openai"
)

// helper: an assistant message carrying one tool call.
func callMsg(id, name, args string) openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{
		Role: "assistant",
		ToolCalls: []openai.ToolCall{{
			ID:       id,
			Function: openai.FunctionCall{Name: name, Arguments: args},
		}},
	}
}

// helper: the tool result for a call.
func resultMsg(id, content string) openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{Role: "tool", ToolCallID: id, Content: content}
}

func TestIndexToolCallsFindsNameAndPath(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		{Role: "user", Content: "fix it"},
		callMsg("c1", "read", `{"path":"parser.go"}`),
		resultMsg("c1", "contents"),
		callMsg("c2", "grep", `{"pattern":"parseExpr"}`),
		resultMsg("c2", "matches"),
	}

	got := indexToolCalls(msgs)

	if got["c1"].name != "read" || got["c1"].path != "parser.go" {
		t.Fatalf("c1 = %+v, want read on parser.go", got["c1"])
	}
	if got["c2"].name != "grep" {
		t.Fatalf("c2 name = %q, want grep", got["c2"].name)
	}
	if got["c2"].path != "" {
		t.Fatalf("c2 path = %q, want empty: grep takes no path argument here", got["c2"].path)
	}
}

// Malformed arguments must not panic or poison the index — a tool call whose
// JSON nib cannot parse simply has no path, and no rule will match it.
func TestIndexToolCallsSurvivesUnparseableArguments(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{callMsg("c1", "read", `{"path":`)}
	got := indexToolCalls(msgs)
	if got["c1"].name != "read" {
		t.Fatalf("name should survive bad args: %+v", got["c1"])
	}
	if got["c1"].path != "" {
		t.Fatalf("path from bad args = %q, want empty", got["c1"].path)
	}
}

// The rule that exists for correctness rather than tokens: once a file is
// edited, an earlier read of it no longer matches disk and the model may reason
// from content that is simply wrong.
func TestStaleReadIDsFlagsAReadInvalidatedByALaterEdit(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"parser.go"}`),
		resultMsg("c1", "old contents"),
		callMsg("c2", "edit", `{"path":"parser.go","old":"a","new":"b"}`),
		resultMsg("c2", "ok"),
	}

	got := staleReadIDs(msgs, indexToolCalls(msgs))

	if !got["c1"] {
		t.Fatal("the read should be stale: parser.go was edited after it")
	}
	if got["c2"] {
		t.Fatal("the edit's own result is not a stale read")
	}
}

// Order matters. A read that happens AFTER the edit is current, and stubbing it
// would throw away the only accurate copy of the file.
func TestStaleReadIDsIgnoresAReadAfterTheEdit(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "edit", `{"path":"parser.go","old":"a","new":"b"}`),
		resultMsg("c1", "ok"),
		callMsg("c2", "read", `{"path":"parser.go"}`),
		resultMsg("c2", "current contents"),
	}

	if staleReadIDs(msgs, indexToolCalls(msgs))["c2"] {
		t.Fatal("a read taken after the edit is current and must not be stubbed")
	}
}

// A different file's edit says nothing about this read.
func TestStaleReadIDsIsPathScoped(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"parser.go"}`),
		resultMsg("c1", "contents"),
		callMsg("c2", "edit", `{"path":"lexer.go","old":"a","new":"b"}`),
		resultMsg("c2", "ok"),
	}

	if staleReadIDs(msgs, indexToolCalls(msgs))["c1"] {
		t.Fatal("editing lexer.go must not invalidate a read of parser.go")
	}
}

// write invalidates a read exactly as edit does — it replaces the file wholesale.
func TestStaleReadIDsTreatsWriteAsInvalidating(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"gen.go"}`),
		resultMsg("c1", "contents"),
		callMsg("c2", "write", `{"path":"gen.go","content":"new"}`),
		resultMsg("c2", "ok"),
	}

	if !staleReadIDs(msgs, indexToolCalls(msgs))["c1"] {
		t.Fatal("a write should invalidate an earlier read of the same file")
	}
}

// Paths that differ only in form refer to the same file.
func TestStaleReadIDsNormalizesPaths(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"./pkg/parser.go"}`),
		resultMsg("c1", "contents"),
		callMsg("c2", "edit", `{"path":"pkg/parser.go","old":"a","new":"b"}`),
		resultMsg("c2", "ok"),
	}

	if !staleReadIDs(msgs, indexToolCalls(msgs))["c1"] {
		t.Fatal("./pkg/parser.go and pkg/parser.go are the same file")
	}
}

// Index 0 is the boundary case, because a missing map entry and a real
// modification at index 0 both read back as the int zero value. Here the SOLE
// invalidating call sits at index 0, so nothing else can put the path into the
// modification index on its behalf.
func TestStaleReadIDsHandlesAnInvalidatingCallAtIndexZero(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "edit", `{"path":"parser.go","old":"a","new":"b"}`),
		resultMsg("c1", "ok"),
		callMsg("c2", "read", `{"path":"parser.go"}`),
		resultMsg("c2", "current contents"),
	}

	if staleReadIDs(msgs, indexToolCalls(msgs))["c2"] {
		t.Fatal("the edit is at index 0, before the read: the read is current")
	}
}

// The mirror of the above: the read is the message at index 0 and the edit
// follows it, so the read IS stale. Together the two pin the comparison at the
// low end of the index range, where off-by-one and zero-value mistakes live.
func TestStaleReadIDsFlagsAReadAtIndexZero(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"parser.go"}`),
		resultMsg("c1", "old contents"),
		callMsg("c2", "edit", `{"path":"parser.go","old":"a","new":"b"}`),
		resultMsg("c2", "ok"),
	}

	if !staleReadIDs(msgs, indexToolCalls(msgs))["c1"] {
		t.Fatal("the read at index 0 was invalidated by the later edit")
	}
}

// A model may emit several tool calls in one assistant message. Both must
// correlate, and both must carry that message's index — the correlation is per
// call, not per message.
func TestIndexToolCallsHandlesMultipleCallsInOneMessage(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		{Role: "user", Content: "look at both"},
		{Role: "assistant", ToolCalls: []openai.ToolCall{
			{ID: "c1", Function: openai.FunctionCall{Name: "read", Arguments: `{"path":"parser.go"}`}},
			{ID: "c2", Function: openai.FunctionCall{Name: "read", Arguments: `{"path":"lexer.go"}`}},
		}},
		resultMsg("c1", "parser contents"),
		resultMsg("c2", "lexer contents"),
	}

	got := indexToolCalls(msgs)

	if len(got) != 2 {
		t.Fatalf("indexed %d calls, want 2: %+v", len(got), got)
	}
	if got["c1"].path != "parser.go" || got["c2"].path != "lexer.go" {
		t.Fatalf("paths = %q, %q, want parser.go, lexer.go", got["c1"].path, got["c2"].path)
	}
	if got["c1"].idx != 1 || got["c2"].idx != 1 {
		t.Fatalf("idx = %d, %d, want both 1: calls share their message's position", got["c1"].idx, got["c2"].idx)
	}
}

// Results are matched by ToolCallID, never by position. Parallel tool calls
// finish in whatever order the tools finish in, so a result may well sit ahead
// of the result of a call that was issued before it.
func TestStaleReadIDsCorrelatesByIDNotPosition(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		{Role: "assistant", ToolCalls: []openai.ToolCall{
			{ID: "c1", Function: openai.FunctionCall{Name: "read", Arguments: `{"path":"parser.go"}`}},
			{ID: "c2", Function: openai.FunctionCall{Name: "read", Arguments: `{"path":"lexer.go"}`}},
		}},
		// Results arrive in the reverse of the order the calls were issued.
		resultMsg("c2", "lexer contents"),
		resultMsg("c1", "parser contents"),
		callMsg("c3", "edit", `{"path":"parser.go","old":"a","new":"b"}`),
		resultMsg("c3", "ok"),
	}

	got := staleReadIDs(msgs, indexToolCalls(msgs))

	if !got["c1"] {
		t.Fatal("c1 read parser.go, which was edited later: stale regardless of where its result sits")
	}
	if got["c2"] {
		t.Fatal("c2 read lexer.go, which nothing edited")
	}
}

// Compaction rewrites history and can drop the assistant message that issued a
// call while its result survives. An orphaned result must be skipped rather
// than crash the pruner — losing the whole session to a nil map entry would be
// a far worse outcome than leaving one result unpruned.
func TestStaleReadIDsSkipsAResultWithNoMatchingCall(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		// No assistant message ever issued "orphan".
		resultMsg("orphan", "contents of something"),
		callMsg("c1", "read", `{"path":"parser.go"}`),
		resultMsg("c1", "old contents"),
		callMsg("c2", "edit", `{"path":"parser.go","old":"a","new":"b"}`),
		resultMsg("c2", "ok"),
	}

	got := staleReadIDs(msgs, indexToolCalls(msgs))

	if got["orphan"] {
		t.Fatal("an orphaned result cannot be shown to be a stale read")
	}
	if !got["c1"] {
		t.Fatal("the orphan must not stop the rest of the history being evaluated")
	}
}

func TestPrunedStubNamesWhatWasDroppedAndHowToGetItBack(t *testing.T) {
	got := prunedStub("read", "parser.go", "250 lines")
	for _, want := range []string{"read", "parser.go", "250 lines", "re-read"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stub %q is missing %q", got, want)
		}
	}
	for _, r := range got {
		if (r >= 0x1F000 && r <= 0x1FAFF) || (r >= 0x2600 && r <= 0x27BF) {
			t.Fatalf("stub contains emoji: %q", got)
		}
	}
}

// bigResult builds a tool result whose estimated size is roughly tokens*4 bytes.
func bigResult(id string, tokens int) openai.ChatCompletionMessage {
	body := make([]byte, tokens*4)
	for i := range body {
		body[i] = 'x'
	}
	return resultMsg(id, string(body))
}

// Below the high-water mark nothing is touched: short sessions must not pay a
// prefix-cache re-prefill for a saving they do not need.
func TestPruneLeavesEverythingAloneBelowHighWater(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"a.go"}`),
		bigResult("c1", 500),
		{Role: "user", Content: "next"},
	}
	cfg := types.ToolOutputPruningConfig{HighWaterTokens: 24000, LowWaterTokens: 8000, MinResultTokens: 200}

	out, newly, freed := pruneToolOutputs(msgs, cfg, map[string]bool{})

	if len(newly) != 0 || freed != 0 {
		t.Fatalf("pruned below high water: newly=%v freed=%d", newly, freed)
	}
	if out[1].Content != msgs[1].Content {
		t.Fatal("content was modified below the high-water mark")
	}
}

// Crossing the mark sweeps oldest-first until under the LOW mark, not merely
// under the high one. Pruning deeply and rarely is what keeps the prefix cache
// useful between sweeps.
func TestPruneSweepsToLowWaterOldestFirst(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"a.go"}`), bigResult("c1", 5000),
		callMsg("c2", "read", `{"path":"b.go"}`), bigResult("c2", 5000),
		callMsg("c3", "read", `{"path":"c.go"}`), bigResult("c3", 5000),
		{Role: "user", Content: "keep going"},
	}
	cfg := types.ToolOutputPruningConfig{HighWaterTokens: 12000, LowWaterTokens: 6000, MinResultTokens: 200}

	out, newly, freed := pruneToolOutputs(msgs, cfg, map[string]bool{})

	if len(newly) != 2 {
		t.Fatalf("newly stubbed = %v, want the two oldest results", newly)
	}
	if newly[0] != "c1" || newly[1] != "c2" {
		t.Fatalf("swept out of order: %v, want [c1 c2]", newly)
	}
	// freed is not merely positive, it is a specific number: Task 5 renders it in
	// a user-facing notice ("freed ~12.4k tokens (estimated)"), so an unverified
	// figure would become a visible lie. It is the size of what was replaced
	// minus the size of what replaced it, in the same byte/4 estimate compact.go's
	// estimateTokens uses. Computed from the fixture and the real stub text so it moves with
	// them, and spelled out as len()/4 rather than via tokensOf so that a broken
	// tokensOf cannot cancel itself out on both sides of the comparison.
	wantFreed := (len(msgs[1].Content)/4 - len(prunedStub("read", "a.go", ""))/4) +
		(len(msgs[3].Content)/4 - len(prunedStub("read", "b.go", ""))/4)
	if freed != wantFreed {
		t.Fatalf("freed = %d, want %d (the two swept bodies minus their stubs)", freed, wantFreed)
	}
	if freed <= 0 {
		t.Fatalf("freed = %d, want a positive token count", freed)
	}
	if out[5].Content != msgs[5].Content {
		t.Fatal("the newest result should survive a sweep to low water")
	}
}

// THE property the whole cache argument rests on. Feed the output back in and
// nothing new is stubbed: the boundary does not move on every call.
func TestPruneIsMonotonic(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"a.go"}`), bigResult("c1", 5000),
		callMsg("c2", "read", `{"path":"b.go"}`), bigResult("c2", 5000),
		callMsg("c3", "read", `{"path":"c.go"}`), bigResult("c3", 5000),
		{Role: "user", Content: "keep going"},
	}
	cfg := types.ToolOutputPruningConfig{HighWaterTokens: 12000, LowWaterTokens: 6000, MinResultTokens: 200}

	already := map[string]bool{}
	_, newly, _ := pruneToolOutputs(msgs, cfg, already)
	for _, id := range newly {
		already[id] = true
	}

	// Same input, same state: a second call must stub nothing further.
	out2, newly2, freed2 := pruneToolOutputs(msgs, cfg, already)
	if len(newly2) != 0 || freed2 != 0 {
		t.Fatalf("second call stubbed more: newly=%v freed=%d — the boundary moves every call and the prefix cache is dead", newly2, freed2)
	}
	// And the previously stubbed results are still stubbed in the output.
	if out2[1].Content == msgs[1].Content {
		t.Fatal("a previously stubbed result came back unstubbed: pruning is not monotonic")
	}

	// The check above is necessary but far too weak on its own: with msgs
	// unchanged, a policy that ignored `already` entirely would re-derive the
	// very same {c1,c2} and still report nothing newly stubbed, because `already`
	// also gates the newly/freed accounting. The property only becomes observable
	// once the conversation GROWS, which it does on every single turn.
	//
	// Seeding target from `already` is what excludes stubbed bodies from the
	// sweep's running total, and that is the hysteresis: c3+c4 is under the high
	// water mark, so no sweep runs and the boundary holds. Counting the stubbed
	// bodies at full size instead would put every later turn back over the mark
	// and walk the boundary one result deeper each call — a prefix-cache
	// invalidation on every request, which is the cost this feature exists to avoid.
	grown := append(append([]openai.ChatCompletionMessage{}, msgs...),
		callMsg("c4", "read", `{"path":"d.go"}`), bigResult("c4", 2000),
		openai.ChatCompletionMessage{Role: "user", Content: "more"},
	)

	out3, newly3, freed3 := pruneToolOutputs(grown, cfg, already)
	if len(newly3) != 0 || freed3 != 0 {
		t.Fatalf("the boundary moved as the conversation grew: newly=%v freed=%d — a sweep that re-derives from full-size stubbed bodies re-prunes on every turn and the prefix cache is dead", newly3, freed3)
	}
	if out3[5].Content != grown[5].Content || out3[8].Content != grown[8].Content {
		t.Fatal("an unstubbed result was stubbed without being reported in newly")
	}
	if out3[1].Content == grown[1].Content || out3[3].Content == grown[3].Content {
		t.Fatal("a previously stubbed result came back unstubbed as the conversation grew")
	}
}

// The trailing run of tool results is what the model is about to reason over.
func TestPruneNeverTouchesTheTrailingToolResults(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"a.go"}`), bigResult("c1", 9000),
		callMsg("c2", "read", `{"path":"b.go"}`), bigResult("c2", 9000),
	}
	cfg := types.ToolOutputPruningConfig{HighWaterTokens: 1000, LowWaterTokens: 1, MinResultTokens: 1}

	out, newly, _ := pruneToolOutputs(msgs, cfg, map[string]bool{})

	for _, id := range newly {
		if id == "c2" {
			t.Fatal("stubbed the trailing result the model is about to read")
		}
	}
	if out[3].Content != msgs[3].Content {
		t.Fatal("trailing tool result was modified")
	}
}

// Small results are not worth a cache invalidation.
func TestPruneRespectsTheMinimumResultSize(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"a.go"}`), bigResult("c1", 50),
		callMsg("c2", "read", `{"path":"b.go"}`), bigResult("c2", 50),
		{Role: "user", Content: "next"},
	}
	cfg := types.ToolOutputPruningConfig{HighWaterTokens: 10, LowWaterTokens: 1, MinResultTokens: 200}

	_, newly, _ := pruneToolOutputs(msgs, cfg, map[string]bool{})
	if len(newly) != 0 {
		t.Fatalf("stubbed results below the floor: %v", newly)
	}
}

// The floor can make the low-water mark unreachable. Best-effort, not a ceiling
// guarantee — and it must stop rather than violate the floor.
func TestPruneStopsAtTheFloorEvenIfLowWaterIsUnreachable(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"a.go"}`), bigResult("c1", 100),
		callMsg("c2", "read", `{"path":"b.go"}`), bigResult("c2", 100),
		{Role: "user", Content: "next"},
	}
	cfg := types.ToolOutputPruningConfig{HighWaterTokens: 50, LowWaterTokens: 1, MinResultTokens: 200}

	_, newly, _ := pruneToolOutputs(msgs, cfg, map[string]bool{})
	if len(newly) != 0 {
		t.Fatalf("violated the floor chasing low water: %v", newly)
	}
}

// A stale read is stubbed regardless of size: the rule is about correctness.
func TestPruneStubsAStaleReadBelowHighWater(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"a.go"}`), bigResult("c1", 300),
		callMsg("c2", "edit", `{"path":"a.go","old":"x","new":"y"}`), resultMsg("c2", "ok"),
		{Role: "user", Content: "next"},
	}
	cfg := types.ToolOutputPruningConfig{HighWaterTokens: 24000, LowWaterTokens: 8000, MinResultTokens: 200}

	_, newly, _ := pruneToolOutputs(msgs, cfg, map[string]bool{})
	if len(newly) != 1 || newly[0] != "c1" {
		t.Fatalf("newly = %v, want the stale read c1 even though we are under high water", newly)
	}
}

func TestPruneDisabledDoesNothing(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"a.go"}`), bigResult("c1", 9000),
		callMsg("c2", "edit", `{"path":"a.go","old":"x","new":"y"}`), resultMsg("c2", "ok"),
		{Role: "user", Content: "next"},
	}
	cfg := types.ToolOutputPruningConfig{Disabled: true, HighWaterTokens: 10, LowWaterTokens: 1, MinResultTokens: 1}

	_, newly, freed := pruneToolOutputs(msgs, cfg, map[string]bool{})
	if len(newly) != 0 || freed != 0 {
		t.Fatalf("disabled pruning still pruned: newly=%v freed=%d", newly, freed)
	}
}

func TestPruneDisableStaleReadsLeavesSizePruningOn(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"a.go"}`), bigResult("c1", 300),
		callMsg("c2", "edit", `{"path":"a.go","old":"x","new":"y"}`), resultMsg("c2", "ok"),
		{Role: "user", Content: "next"},
	}
	cfg := types.ToolOutputPruningConfig{DisableStaleReads: true, HighWaterTokens: 24000, LowWaterTokens: 8000, MinResultTokens: 200}

	_, newly, _ := pruneToolOutputs(msgs, cfg, map[string]bool{})
	if len(newly) != 0 {
		t.Fatalf("stale-read rule fired while disabled: %v", newly)
	}
}

// high_water_tokens: 0 is the documented way to keep the correctness rule while
// turning size pruning off.
func TestPruneZeroHighWaterDisablesOnlyTheSweep(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"a.go"}`), bigResult("c1", 9000),
		callMsg("c2", "read", `{"path":"b.go"}`), bigResult("c2", 9000),
		callMsg("c3", "edit", `{"path":"a.go","old":"x","new":"y"}`), resultMsg("c3", "ok"),
		{Role: "user", Content: "next"},
	}
	cfg := types.ToolOutputPruningConfig{HighWaterTokens: 0, LowWaterTokens: 8000, MinResultTokens: 200}

	_, newly, _ := pruneToolOutputs(msgs, cfg, map[string]bool{})
	if len(newly) != 1 || newly[0] != "c1" {
		t.Fatalf("newly = %v, want only the stale read: the sweep is off but correctness is not", newly)
	}
}

// The invariant that keeps the next API request valid.
func TestPrunePreservesMessageCountRolesAndToolCallIDs(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"a.go"}`), bigResult("c1", 9000),
		callMsg("c2", "read", `{"path":"b.go"}`), bigResult("c2", 9000),
		{Role: "user", Content: "next"},
	}
	cfg := types.ToolOutputPruningConfig{HighWaterTokens: 1000, LowWaterTokens: 1, MinResultTokens: 1}

	out, _, _ := pruneToolOutputs(msgs, cfg, map[string]bool{})

	if len(out) != len(msgs) {
		t.Fatalf("message count changed %d -> %d: an orphaned tool_calls message makes the next request invalid", len(msgs), len(out))
	}
	for i := range msgs {
		if out[i].Role != msgs[i].Role {
			t.Fatalf("message %d role changed %q -> %q", i, msgs[i].Role, out[i].Role)
		}
		if out[i].ToolCallID != msgs[i].ToolCallID {
			t.Fatalf("message %d ToolCallID changed %q -> %q", i, msgs[i].ToolCallID, out[i].ToolCallID)
		}
		if len(out[i].ToolCalls) != len(msgs[i].ToolCalls) {
			t.Fatalf("message %d tool calls changed", i)
		}
	}
}

// The manipulator gets the slice cogito is about to send. Writing through it
// would corrupt the caller's fragment, which pruning must never touch.
func TestPruneDoesNotMutateTheInput(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"a.go"}`), bigResult("c1", 9000),
		callMsg("c2", "read", `{"path":"b.go"}`), bigResult("c2", 9000),
		{Role: "user", Content: "next"},
	}
	before := msgs[1].Content
	cfg := types.ToolOutputPruningConfig{HighWaterTokens: 1000, LowWaterTokens: 1, MinResultTokens: 1}

	_, _, _ = pruneToolOutputs(msgs, cfg, map[string]bool{})

	if msgs[1].Content != before {
		t.Fatal("pruneToolOutputs wrote through to the caller's slice")
	}
}

// Compaction can drop the assistant message that issued a call while its result
// survives. Task 2 already proved staleReadIDs skips such an orphan, but the
// SWEEP has no reason to skip it — it is a large tool result like any other. The
// stub it gets is text the model reads, so it must still name something.
func TestPruneStubsAnOrphanedResultReadably(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		// No assistant message ever issued "orphan".
		bigResult("orphan", 5000),
		callMsg("c1", "read", `{"path":"a.go"}`), bigResult("c1", 5000),
		{Role: "user", Content: "next"},
	}
	cfg := types.ToolOutputPruningConfig{HighWaterTokens: 6000, LowWaterTokens: 1, MinResultTokens: 200}

	out, newly, _ := pruneToolOutputs(msgs, cfg, map[string]bool{})

	if len(newly) == 0 || newly[0] != "orphan" {
		t.Fatalf("newly = %v, want the orphan swept like any other oversized result", newly)
	}
	if strings.Contains(out[0].Content, "[ ") || strings.Contains(out[0].Content, "[—") {
		t.Fatalf("orphan stub has an empty tool name: %q", out[0].Content)
	}
	if !strings.Contains(out[0].Content, "tool") {
		t.Fatalf("orphan stub names nothing the model can act on: %q", out[0].Content)
	}
}

// A result below the floor must not STALL the sweep. Both floor tests above use
// uniformly small results, so they pass whether the sweep skips a small result
// and keeps going or stops dead at the first one it sees. Here a small OLD
// result sits ahead of a large one: the sweep has to step over c1 and still
// reach c2, or the one result actually worth stubbing is never found.
func TestPruneSkipsASmallResultWithoutStallingTheSweep(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		callMsg("c1", "read", `{"path":"a.go"}`), bigResult("c1", 50),
		callMsg("c2", "read", `{"path":"b.go"}`), bigResult("c2", 5000),
		{Role: "user", Content: "next"},
	}
	cfg := types.ToolOutputPruningConfig{HighWaterTokens: 1000, LowWaterTokens: 1, MinResultTokens: 200}

	out, newly, _ := pruneToolOutputs(msgs, cfg, map[string]bool{})

	if len(newly) != 1 || newly[0] != "c2" {
		t.Fatalf("newly = %v, want [c2]: a small old result must not stop the sweep reaching a large newer one", newly)
	}
	if out[1].Content != msgs[1].Content {
		t.Fatal("stubbed the small result that sits below the floor")
	}
}

// Monotonicity has a second, quieter half. The trailing run is protected from
// being stubbed, but that protection must NOT reach back and un-stub something
// already stubbed: compaction rewrites history, and a result that was stubbed
// turns ago can end up back inside the trailing run. Dropping it from the target
// set there would hand the model back a body it had already lost — a
// stubbed->kept transition, the one direction the policy must never move, and a
// prefix-cache invalidation on top.
//
// The parallel tool calls are what put two results next to each other at the end
// of the conversation, which is what makes c1 trailing AND previously stubbed.
func TestPruneKeepsAStubbedResultStubbedInsideTheTrailingRun(t *testing.T) {
	msgs := []openai.ChatCompletionMessage{
		{Role: "assistant", ToolCalls: []openai.ToolCall{
			{ID: "c1", Function: openai.FunctionCall{Name: "read", Arguments: `{"path":"a.go"}`}},
			{ID: "c2", Function: openai.FunctionCall{Name: "read", Arguments: `{"path":"b.go"}`}},
		}},
		bigResult("c1", 5000),
		bigResult("c2", 5000),
	}
	// Well under the high-water mark, so the sweep never runs and this test
	// isolates the `already` seeding.
	cfg := types.ToolOutputPruningConfig{HighWaterTokens: 24000, LowWaterTokens: 8000, MinResultTokens: 200}

	already := map[string]bool{"c1": true}

	out, newly, freed := pruneToolOutputs(msgs, cfg, already)

	if out[1].Content == msgs[1].Content {
		t.Fatal("a previously stubbed result came back unstubbed because it is now in the trailing run: pruning is not monotonic")
	}
	if len(newly) != 0 || freed != 0 {
		t.Fatalf("nothing new should be stubbed here: newly=%v freed=%d", newly, freed)
	}
	// The protection still does its job for results that were never stubbed.
	if out[2].Content != msgs[2].Content {
		t.Fatal("stubbed a trailing result that had never been stubbed before")
	}
}
