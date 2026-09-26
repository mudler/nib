package chat

import (
	"errors"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func vmsg(role string, n int) openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{Role: role, Content: strings.Repeat("x", n*4)}
}

func vcall(id string) openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{
		Role: openai.ChatMessageRoleAssistant,
		ToolCalls: []openai.ToolCall{{
			ID: id, Type: openai.ToolTypeFunction,
			Function: openai.FunctionCall{Name: "read", Arguments: "{}"},
		}},
	}
}

func vresult(id string) openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{Role: openai.ChatMessageRoleTool, ToolCallID: id, Content: "ok"}
}

func TestValidateCompactionAcceptsAValidResult(t *testing.T) {
	before := []openai.ChatCompletionMessage{vmsg("user", 100), vcall("a"), vresult("a"), vmsg("assistant", 100)}
	after := []openai.ChatCompletionMessage{vmsg("user", 10), vcall("a"), vresult("a")}
	if err := validateCompaction(before, after, 1000); err != nil {
		t.Fatalf("valid compaction rejected: %v", err)
	}
}

func TestValidateCompactionRejectsEmpty(t *testing.T) {
	before := []openai.ChatCompletionMessage{vmsg("user", 100)}
	if err := validateCompaction(before, nil, 1000); !errors.Is(err, ErrCompactionEmpty) {
		t.Fatalf("err = %v, want ErrCompactionEmpty", err)
	}
}

func TestValidateCompactionRejectsGrowth(t *testing.T) {
	before := []openai.ChatCompletionMessage{vmsg("user", 10)}
	after := []openai.ChatCompletionMessage{vmsg("user", 20)}
	if err := validateCompaction(before, after, 1000); !errors.Is(err, ErrCompactionGrew) {
		t.Fatalf("err = %v, want ErrCompactionGrew", err)
	}
}

func TestValidateCompactionRejectsNoOp(t *testing.T) {
	before := []openai.ChatCompletionMessage{vmsg("user", 50), vmsg("assistant", 50)}
	after := []openai.ChatCompletionMessage{vmsg("user", 100)}
	if err := validateCompaction(before, after, 1000); !errors.Is(err, ErrCompactionNoOp) {
		t.Fatalf("err = %v, want ErrCompactionNoOp", err)
	}
}

func TestValidateCompactionRejectsNoOpFromSplit(t *testing.T) {
	// A split whose head comes back unchanged reassembles into the original
	// history, the same size: the known no-op cause.
	before := []openai.ChatCompletionMessage{vmsg("user", 50), vmsg("assistant", 50)}
	head, tail := splitForCompaction(before, 1)
	after := append(append([]openai.ChatCompletionMessage{}, head...), tail...)
	if err := validateCompaction(before, after, 1000); !errors.Is(err, ErrCompactionNoOp) {
		t.Fatalf("err = %v, want ErrCompactionNoOp", err)
	}
}

func TestValidateCompactionRejectsOverBudget(t *testing.T) {
	before := []openai.ChatCompletionMessage{vmsg("user", 500)}
	after := []openai.ChatCompletionMessage{vmsg("user", 200)}
	if err := validateCompaction(before, after, 100); !errors.Is(err, ErrCompactionOverBudget) {
		t.Fatalf("err = %v, want ErrCompactionOverBudget", err)
	}
}

func TestValidateCompactionGrowthBeatsNoOpAndBudget(t *testing.T) {
	before := []openai.ChatCompletionMessage{vmsg("user", 10)}
	after := []openai.ChatCompletionMessage{vmsg("user", 500)}
	if err := validateCompaction(before, after, 100); !errors.Is(err, ErrCompactionGrew) {
		t.Fatalf("err = %v, want ErrCompactionGrew", err)
	}
}

func TestValidateCompactionRejectsOrphanToolResult(t *testing.T) {
	before := []openai.ChatCompletionMessage{vmsg("user", 100), vcall("a"), vresult("a")}
	after := []openai.ChatCompletionMessage{vmsg("user", 10), vresult("a")}
	err := validateCompaction(before, after, 1000)
	if err == nil || errors.Is(err, ErrCompactionEmpty) || errors.Is(err, ErrCompactionGrew) ||
		errors.Is(err, ErrCompactionNoOp) || errors.Is(err, ErrCompactionOverBudget) {
		t.Fatalf("err = %v, want a tool-pairing error", err)
	}
}

func TestValidateCompactionRejectsOrphanToolCall(t *testing.T) {
	before := []openai.ChatCompletionMessage{vmsg("user", 100), vcall("a"), vresult("a")}
	after := []openai.ChatCompletionMessage{vmsg("user", 10), vcall("a")}
	if err := validateCompaction(before, after, 1000); err == nil {
		t.Fatal("orphan tool call accepted")
	}
}

func TestValidateCompactionToolPairing(t *testing.T) {
	cases := []struct {
		name string
		msgs []openai.ChatCompletionMessage
		ok   bool
	}{
		{"paired", []openai.ChatCompletionMessage{vcall("a"), vresult("a")}, true},
		{"no tools", []openai.ChatCompletionMessage{vmsg("user", 1)}, true},
		{"orphan result", []openai.ChatCompletionMessage{vresult("a")}, false},
		{"result before call", []openai.ChatCompletionMessage{vresult("a"), vcall("a")}, false},
		{"orphan call", []openai.ChatCompletionMessage{vcall("a"), vmsg("user", 1)}, false},
		{"mismatched id", []openai.ChatCompletionMessage{vcall("a"), vresult("b")}, false},
	}
	for _, c := range cases {
		err := validateToolPairing(c.msgs)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}
