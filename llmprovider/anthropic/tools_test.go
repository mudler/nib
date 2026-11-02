package anthropic

import (
	"encoding/json"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestTranslateToolsNeverSendsNullInputSchema(t *testing.T) {
	var nilRaw json.RawMessage
	tools := []openai.Tool{
		{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "no_params"}},
		{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "typed_nil", Parameters: nilRaw}},
		{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{
			Name:       "with_params",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}},
		}},
	}

	out := translateTools(tools)
	if len(out) != len(tools) {
		t.Fatalf("got %d tools, want %d", len(out), len(tools))
	}
	for _, tool := range out {
		b, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("%s: marshal: %v", tool.Name, err)
		}
		var wire struct {
			InputSchema map[string]any `json:"input_schema"`
		}
		if err := json.Unmarshal(b, &wire); err != nil {
			t.Fatalf("%s: unmarshal: %v", tool.Name, err)
		}
		if wire.InputSchema == nil {
			t.Fatalf("%s: input_schema is not an object: %s", tool.Name, b)
		}
		if wire.InputSchema["type"] != "object" {
			t.Fatalf("%s: input_schema type = %v, want object", tool.Name, wire.InputSchema["type"])
		}
	}

	b, _ := json.Marshal(out[2])
	var wire struct {
		InputSchema struct {
			Properties map[string]any `json:"properties"`
		} `json:"input_schema"`
	}
	_ = json.Unmarshal(b, &wire)
	if _, ok := wire.InputSchema.Properties["q"]; !ok {
		t.Fatalf("with_params: declared schema was replaced: %s", b)
	}
}
