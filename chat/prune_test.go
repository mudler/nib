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
