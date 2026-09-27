package chat_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
	openai "github.com/sashabaranov/go-openai"
)

// resumeContextWithReads is a stored model context holding n read results of
// size bytes each, followed by a final answer.
func resumeContextWithReads(n, size int) []openai.ChatCompletionMessage {
	msgs := []openai.ChatCompletionMessage{{Role: "user", Content: "read the files"}}
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("r%d", i)
		msgs = append(msgs,
			openai.ChatCompletionMessage{Role: "assistant", ToolCalls: []openai.ToolCall{{
				ID: id, Type: "function",
				Function: openai.FunctionCall{Name: "read", Arguments: fmt.Sprintf(`{"path":"/nonexistent/f%d.go"}`, i)},
			}}},
			openai.ChatCompletionMessage{Role: "tool", ToolCallID: id, Content: id + ":" + strings.Repeat("x", size)},
		)
	}
	return append(msgs, openai.ChatCompletionMessage{Role: "assistant", Content: "read them all"})
}

// TestResumeKeepsPruningState: a session that had stubbed and compressed tool
// results before it was recorded must send the same request after a resume:
// the same stubs, the same shortened text, and nothing stubbed in addition.
// Without the state, the first request sends the stubbed and shortened results
// in full again, and the pruning decisions start over.
func TestResumeKeepsPruningState(t *testing.T) {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("error"), ""))

	var mu sync.Mutex
	var last []capturedMessage
	srv := httptest.NewServer(messageCapturingOpenAI(func(m []capturedMessage) {
		mu.Lock()
		last = m
		mu.Unlock()
	}))
	defer srv.Close()

	// Four reads of about 3000 tokens each (byte/4). Before the quit, the
	// sweep had stubbed r1 and progressive compression had shortened r4. The
	// results left in full stay under the high-water mark, so no further
	// sweep is due.
	stub := chat.PrunedStubForTest("read", "/nonexistent/f1.go", chat.DetailBudgetForTest)
	state := types.PruningState{
		Pruned:     map[string]string{"r1": chat.DetailBudgetForTest},
		Compressed: map[string]types.CompressedResult{"r4": {Level: int(chat.CompressionTruncated), Content: "COMPRESSED-R4"}},
	}

	cfg := types.Config{
		Model:        "fake-model",
		APIKey:       "fake-key",
		BaseURL:      srv.URL + "/v1",
		LogLevel:     "error",
		ApprovalMode: "auto",
		AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 3, MaxRetries: 3},
		ToolOutputPruning: types.ToolOutputPruningConfig{
			HighWaterTokens: 10000,
			LowWaterTokens:  6000,
		},
		InitialHistory: []openai.ChatCompletionMessage{
			{Role: "user", Content: "read the files"},
			{Role: "assistant", Content: "read them all"},
		},
		InitialContext: resumeContextWithReads(4, 12000),
		InitialPruning: state,
	}
	session, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	if _, err := session.SendMessage("go on"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	mu.Lock()
	got := last
	mu.Unlock()

	var tools []string
	for _, m := range got {
		if m.Role == "tool" {
			tools = append(tools, m.Content)
		}
	}
	if len(tools) != 4 {
		t.Fatalf("request has %d tool results, want 4: %+v", len(tools), got)
	}
	if tools[0] != stub {
		t.Errorf("r1 = %q, want the restored stub %q", short(tools[0]), stub)
	}
	for i, id := range []string{"r2", "r3"} {
		if !strings.HasPrefix(tools[i+1], id+":xxx") {
			t.Errorf("%s = %q, want it in full as before the quit", id, short(tools[i+1]))
		}
	}
	if tools[3] != "COMPRESSED-R4" {
		t.Errorf("r4 = %q, want the restored compressed text", short(tools[3]))
	}

	// The state a resumed session records again is the one it was given.
	exp := session.ExportPruning()
	if exp.Pruned["r1"] != chat.DetailBudgetForTest || exp.Compressed["r4"].Content != "COMPRESSED-R4" {
		t.Errorf("ExportPruning = %+v", exp)
	}
}

func short(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}
