package chat

import (
	"strings"
	"testing"

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
