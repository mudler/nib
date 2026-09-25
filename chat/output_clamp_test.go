package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/types"
	"github.com/sashabaranov/go-openai"
)

// promptOf builds a request whose estimated prompt is about tokens tokens
// (estimateTokens counts bytes/4).
func promptOf(tokens int) openai.ChatCompletionRequest {
	return openai.ChatCompletionRequest{
		Messages: []openai.ChatCompletionMessage{{Role: "user", Content: strings.Repeat("a", tokens*4)}},
	}
}

func TestClampOutputLeavesRoomForThePrompt(t *testing.T) {
	got := clampOutputTokens(promptOf(70000), 96000, 96000)
	want := 96000 - 70000 - outputSafetyMargin
	if got.MaxTokens != want {
		t.Fatalf("MaxTokens = %d, want %d", got.MaxTokens, want)
	}
}

func TestClampOutputNeverAboveCap(t *testing.T) {
	got := clampOutputTokens(promptOf(100), 8000, 96000)
	if got.MaxTokens != 8000 {
		t.Fatalf("MaxTokens = %d, want the cap 8000", got.MaxTokens)
	}
}

func TestClampOutputFloorWhenPromptFillsWindow(t *testing.T) {
	got := clampOutputTokens(promptOf(120000), 96000, 96000)
	if got.MaxTokens != minOutputTokens {
		t.Fatalf("MaxTokens = %d, want minOutputTokens %d", got.MaxTokens, minOutputTokens)
	}
}

func TestClampOutputLeavesExplicitValueAlone(t *testing.T) {
	req := promptOf(70000)
	req.MaxTokens = 50000
	if got := clampOutputTokens(req, 96000, 96000); got.MaxTokens != 50000 || got.MaxCompletionTokens != 0 {
		t.Fatalf("explicit MaxTokens changed: %+v", got.MaxTokens)
	}
	req = promptOf(70000)
	req.MaxCompletionTokens = 50000
	if got := clampOutputTokens(req, 96000, 96000); got.MaxTokens != 0 || got.MaxCompletionTokens != 50000 {
		t.Fatalf("explicit MaxCompletionTokens changed: %d/%d", got.MaxTokens, got.MaxCompletionTokens)
	}
}

func TestClampOutputUnknownLimitsLeaveRequestAlone(t *testing.T) {
	if got := clampOutputTokens(promptOf(70000), 0, 96000); got.MaxTokens != 0 {
		t.Fatalf("cap 0: MaxTokens = %d, want 0", got.MaxTokens)
	}
	if got := clampOutputTokens(promptOf(70000), 96000, 0); got.MaxTokens != 0 {
		t.Fatalf("window 0: MaxTokens = %d, want 0", got.MaxTokens)
	}
}

func TestClampOutputCountsToolSchemas(t *testing.T) {
	req := promptOf(10000)
	without := clampOutputTokens(req, 96000, 96000).MaxTokens

	req.Tools = []openai.Tool{{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        "big",
			Description: strings.Repeat("d", 40000), // ~10k tokens of schema
		},
	}}
	with := clampOutputTokens(req, 96000, 96000).MaxTokens
	if without-with < 9000 {
		t.Fatalf("tool schemas not counted: without=%d with=%d", without, with)
	}
}

// captureLLMReq records the request it receives, on both paths.
type captureLLMReq struct {
	got    openai.ChatCompletionRequest
	stream openai.ChatCompletionRequest
}

func (c *captureLLMReq) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	return f, nil
}

func (c *captureLLMReq) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	c.got = req
	return cogito.LLMReply{}, cogito.LLMUsage{}, nil
}

func (c *captureLLMReq) CreateChatCompletionStream(ctx context.Context, req openai.ChatCompletionRequest) (<-chan cogito.StreamEvent, error) {
	c.stream = req
	ch := make(chan cogito.StreamEvent)
	close(ch)
	return ch, nil
}

func TestClampOutputAppliedByTrackedLLM(t *testing.T) {
	inner := &captureLLMReq{}
	var live liveUsage
	llm := trackUsage(inner, &live, func() (int, int) { return 96000, 96000 })

	if _, _, err := llm.CreateChatCompletion(context.Background(), promptOf(70000)); err != nil {
		t.Fatal(err)
	}
	want := 96000 - 70000 - outputSafetyMargin
	if inner.got.MaxTokens != want {
		t.Fatalf("blocking MaxTokens = %d, want %d", inner.got.MaxTokens, want)
	}

	sl, ok := llm.(cogito.StreamingLLM)
	if !ok {
		t.Fatal("wrapper lost streaming support")
	}
	ch, err := sl.CreateChatCompletionStream(context.Background(), promptOf(70000))
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if inner.stream.MaxTokens != want {
		t.Fatalf("streaming MaxTokens = %d, want %d", inner.stream.MaxTokens, want)
	}
}

func TestClampOutputNilLimitsIsNoop(t *testing.T) {
	inner := &captureLLMReq{}
	var live liveUsage
	llm := trackUsage(inner, &live, nil)
	if _, _, err := llm.CreateChatCompletion(context.Background(), promptOf(70000)); err != nil {
		t.Fatal(err)
	}
	if inner.got.MaxTokens != 0 {
		t.Fatalf("MaxTokens = %d, want untouched 0", inner.got.MaxTokens)
	}
}

func TestClampOutputSessionLimits(t *testing.T) {
	s := &Session{
		llmModel:   "m",
		outputCap:  96000,
		compaction: types.CompactionConfig{MaxContextTokens: 96000},
	}
	if c, w := s.requestLimits(); c != 96000 || w != 96000 {
		t.Fatalf("requestLimits = %d,%d", c, w)
	}
	s.learnedWindow, s.learnedWindowModel = 210000, "m"
	if _, w := s.requestLimits(); w != 210000 {
		t.Fatalf("learned window not used: %d", w)
	}
}

// capSetterLLM is a client that carries an output cap, like LocalAIClient.
type capSetterLLM struct{ captureLLMReq }

func (c *capSetterLLM) SetMaxTokens(int) {}

func TestClampOutputFactoryCapRecorded(t *testing.T) {
	p := types.ModelProviderConfig{Model: "some-model", MaxTokens: 96000, BaseURL: "http://localhost:1/v1"}
	if got := factoryOutputCap(&capSetterLLM{}, p, nil); got != 96000 {
		t.Fatalf("factoryOutputCap = %d, want 96000", got)
	}
	if got := factoryOutputCap(&captureLLMReq{}, p, nil); got != 0 {
		t.Fatalf("adapter without a cap: factoryOutputCap = %d, want 0", got)
	}
}
