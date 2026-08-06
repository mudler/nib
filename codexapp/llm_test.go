package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

func TestLiveChatGPTSubscription(t *testing.T) {
	if os.Getenv("NIB_CODEX_APP_SERVER_INTEGRATION") == "" {
		t.Skip("set NIB_CODEX_APP_SERVER_INTEGRATION=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	llm := New(Config{})
	reply, _, err := llm.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: "Return one JSON object with the boolean field ok set to true. Do not use tools."},
			{Role: openai.ChatMessageRoleUser, Content: "Produce the requested object."},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject},
	})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal([]byte(reply.ChatCompletionResponse.Choices[0].Message.Content), &result); err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatalf("unexpected response: %s", reply.ChatCompletionResponse.Choices[0].Message.Content)
	}
}

func TestCreateChatCompletionUsesIsolatedReadOnlyTurn(t *testing.T) {
	llm := New(Config{Command: os.Args[0], Args: []string{"-test.run=TestAppServerHelper", "--"}, Model: "codex-test"})
	reply, _, err := llm.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: "Return only JSON."},
			{Role: openai.ChatMessageRoleUser, Content: "classify this"},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := reply.ChatCompletionResponse.Choices[0].Message.Content; got != `{"spans":[]}` {
		t.Fatalf("content = %q", got)
	}
}

func TestDefaultCommandInvokesCodexDirectly(t *testing.T) {
	llm := New(Config{})
	if llm.config.Command != "codex" {
		t.Fatalf("command = %q", llm.config.Command)
	}
	want := []string{"app-server", "--stdio"}
	if len(llm.config.Args) != len(want) || llm.config.Args[0] != want[0] || llm.config.Args[1] != want[1] {
		t.Fatalf("args = %#v", llm.config.Args)
	}
}

func TestAppServerHelper(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "--" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(2)
		}
		switch request.Method {
		case "initialize":
			_ = enc.Encode(map[string]any{"id": request.ID, "result": map[string]any{"userAgent": "fake"}})
		case "initialized":
		case "thread/start":
			if request.Params["ephemeral"] != true || request.Params["approvalPolicy"] != "never" || request.Params["sandbox"] != "read-only" {
				os.Exit(3)
			}
			if request.Params["baseInstructions"] != "Return only JSON." {
				os.Exit(4)
			}
			_ = enc.Encode(map[string]any{"id": request.ID, "result": map[string]any{"thread": map[string]any{"id": "thread-1"}}})
		case "turn/start":
			policy, _ := request.Params["sandboxPolicy"].(map[string]any)
			if policy["type"] != "readOnly" || policy["networkAccess"] != false {
				os.Exit(5)
			}
			_ = enc.Encode(map[string]any{"id": request.ID, "result": map[string]any{"turn": map[string]any{"id": "turn-1"}}})
			_ = enc.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": map[string]any{"type": "agentMessage", "text": `{"spans":[]}`}}})
			_ = enc.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-1", "turn": map[string]any{"id": "turn-1", "status": "completed"}}})
		default:
			fmt.Fprintln(os.Stderr, "unexpected method", request.Method)
			os.Exit(6)
		}
	}
}
