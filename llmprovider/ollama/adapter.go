// Package ollama adapts the Ollama native chat API (/api/chat) to cogito.LLM.
//
// Ollama exposes both an OpenAI-compatible endpoint (/v1/chat/completions)
// and its native API (/api/chat). The native API supports the `thinking`
// field for reasoning models and uses a distinct tool-call / tool-result
// format. This adapter follows the same pattern as the anthropic adapter:
// translate openai.ChatCompletionRequest → Ollama /api/chat, parse the
// response back into openai types, and wrap in cogito.LLMReply.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/internal/fragmentreply"
	openai "github.com/sashabaranov/go-openai"
)

// Config holds the connection + auth parameters for the Ollama adapter.
type Config struct {
	Model   string
	BaseURL string // default "http://127.0.0.1:11434"
	APIKey  string // optional; local Ollama needs no key
}

// LLM implements cogito.LLM against the Ollama /api/chat endpoint.
type LLM struct {
	config Config
	client *http.Client
}

var _ cogito.LLM = (*LLM)(nil)

// New returns an Ollama adapter. If BaseURL is empty, defaults to
// http://127.0.0.1:11434.
func New(config Config) *LLM {
	if config.BaseURL == "" {
		config.BaseURL = "http://127.0.0.1:11434"
	}
	return &LLM{
		config: config,
		client: &http.Client{},
	}
}

// Ask delegates to CreateChatCompletion, mirroring the codexapp pattern.
func (l *LLM) Ask(ctx context.Context, fragment cogito.Fragment) (cogito.Fragment, error) {
	reply, usage, err := l.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    l.config.Model,
		Messages: fragment.GetMessages(),
	})
	if err != nil {
		return fragment, err
	}
	if len(reply.ChatCompletionResponse.Choices) == 0 {
		return fragment, ErrNoResponse
	}
	fragment = fragmentreply.Append(fragment, reply.ChatCompletionResponse.Choices[0].Message)
	if fragment.Status != nil {
		fragment.Status.LastUsage = usage
		fragment.Status.CumulativeUsage.PromptTokens += usage.PromptTokens
		fragment.Status.CumulativeUsage.CompletionTokens += usage.CompletionTokens
		fragment.Status.CumulativeUsage.TotalTokens += usage.TotalTokens
	}
	return fragment, nil
}

// ErrNoResponse is returned when the API returns a message with no content.
var ErrNoResponse = errors.New("ollama: completed without an assistant message")

// CreateChatCompletion translates an OpenAI Chat Completion request to the
// Ollama /api/chat API, calls it, and translates the response back.
func (l *LLM) CreateChatCompletion(ctx context.Context, request openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	body, err := l.translateRequest(request)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}

	url := strings.TrimRight(l.config.BaseURL, "/") + "/api/chat"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("ollama: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if l.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+l.config.APIKey)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("ollama: request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("ollama: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return cogito.LLMReply{}, cogito.LLMUsage{}, parseAPIError(resp.StatusCode, respBody)
	}

	return l.translateResponse(respBody, request.Model)
}

// ---------------------------------------------------------------------------
// Request translation: openai.ChatCompletionRequest → Ollama /api/chat body
// ---------------------------------------------------------------------------

// ollamaRequest is the JSON body for POST /api/chat.
type ollamaRequest struct {
	Model      string          `json:"model"`
	Messages   []ollamaMessage `json:"messages"`
	Tools      []ollamaTool    `json:"tools,omitempty"`
	ToolChoice string          `json:"tool_choice,omitempty"`
	Options    ollamaOptions   `json:"options,omitempty"`
	Stream     bool            `json:"stream"`
}

type ollamaOptions struct {
	NumPredict  int     `json:"num_predict,omitempty"`
	Temperature float32 `json:"temperature,omitempty"`
	TopP        float32 `json:"top_p,omitempty"`
}

type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	Thinking  string           `json:"thinking,omitempty"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
	ToolName  string           `json:"tool_name,omitempty"`
	Images    []string         `json:"images,omitempty"`
}

type ollamaTool struct {
	Type     string             `json:"type"` // always "function"
	Function ollamaToolFunction `json:"function"`
}

type ollamaToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ollamaToolCall is a tool call in an assistant message.
type ollamaToolCall struct {
	Type     string                 `json:"type"` // always "function"
	Function ollamaToolCallFunction `json:"function"`
}

type ollamaToolCallFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func (l *LLM) translateRequest(req openai.ChatCompletionRequest) ([]byte, error) {
	or := ollamaRequest{
		Model:    firstNonEmpty(req.Model, l.config.Model),
		Stream:   false,
		Messages: make([]ollamaMessage, 0, len(req.Messages)),
	}

	// Build a tool-call-ID → function-name map from assistant messages so we
	// can populate Ollama's `tool_name` field on tool-result messages. OpenAI
	// format only carries tool_call_id, but Ollama needs the function name.
	toolCallNames := make(map[string]string)
	for _, msg := range req.Messages {
		if msg.Role == openai.ChatMessageRoleAssistant {
			for _, tc := range msg.ToolCalls {
				toolCallNames[tc.ID] = tc.Function.Name
			}
		}
	}

	for _, msg := range req.Messages {
		switch msg.Role {
		case openai.ChatMessageRoleSystem, openai.ChatMessageRoleDeveloper:
			if msg.Content != "" {
				or.Messages = append(or.Messages, ollamaMessage{Role: "system", Content: msg.Content})
			}
		case openai.ChatMessageRoleUser:
			or.Messages = append(or.Messages, translateOllamaUserMessage(msg))
		case openai.ChatMessageRoleAssistant:
			or.Messages = append(or.Messages, translateOllamaAssistantMessage(msg))
		case openai.ChatMessageRoleTool:
			toolName := toolCallNames[msg.ToolCallID]
			or.Messages = append(or.Messages, ollamaMessage{
				Role:     "tool",
				Content:  msg.Content,
				ToolName: toolName,
			})
		default:
			or.Messages = append(or.Messages, ollamaMessage{Role: "user", Content: msg.Content})
		}
	}

	if len(req.Tools) > 0 {
		or.Tools = translateOllamaTools(req.Tools)
	}
	if req.ToolChoice != nil {
		if tc := translateOllamaToolChoice(req.ToolChoice); tc != "" {
			or.ToolChoice = tc
		}
	}

	if req.MaxTokens > 0 {
		or.Options.NumPredict = req.MaxTokens
	}
	if req.MaxCompletionTokens > 0 {
		or.Options.NumPredict = req.MaxCompletionTokens
	}
	if req.Temperature > 0 {
		or.Options.Temperature = req.Temperature
	}
	if req.TopP > 0 {
		or.Options.TopP = req.TopP
	}

	data, err := json.Marshal(or)
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}
	return data, nil
}

func translateOllamaUserMessage(msg openai.ChatCompletionMessage) ollamaMessage {
	if len(msg.MultiContent) == 0 {
		return ollamaMessage{Role: "user", Content: msg.Content}
	}
	var textParts []string
	var images []string
	for _, part := range msg.MultiContent {
		switch part.Type {
		case openai.ChatMessagePartTypeText:
			textParts = append(textParts, part.Text)
		case openai.ChatMessagePartTypeImageURL:
			if img := extractBase64Image(part.ImageURL.URL); img != "" {
				images = append(images, img)
			}
		}
	}
	return ollamaMessage{
		Role:    "user",
		Content: strings.Join(textParts, "\n"),
		Images:  images,
	}
}

func translateOllamaAssistantMessage(msg openai.ChatCompletionMessage) ollamaMessage {
	om := ollamaMessage{Role: "assistant", Content: msg.Content}
	if msg.ReasoningContent != "" {
		om.Thinking = msg.ReasoningContent
	}
	if len(msg.ToolCalls) > 0 {
		om.ToolCalls = make([]ollamaToolCall, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			var args json.RawMessage
			if tc.Function.Arguments != "" {
				args = json.RawMessage(tc.Function.Arguments)
			}
			om.ToolCalls = append(om.ToolCalls, ollamaToolCall{
				Type: "function",
				Function: ollamaToolCallFunction{
					Name:      tc.Function.Name,
					Arguments: args,
				},
			})
		}
	}
	return om
}

func translateOllamaTools(tools []openai.Tool) []ollamaTool {
	out := make([]ollamaTool, 0, len(tools))
	for _, t := range tools {
		if t.Function == nil {
			continue
		}
		var params json.RawMessage
		if t.Function.Parameters != nil {
			if data, err := json.Marshal(t.Function.Parameters); err == nil {
				params = data
			}
		}
		out = append(out, ollamaTool{
			Type: "function",
			Function: ollamaToolFunction{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  params,
			},
		})
	}
	return out
}

func translateOllamaToolChoice(choice any) string {
	switch v := choice.(type) {
	case string:
		switch v {
		case "auto":
			return "auto"
		case "none":
			return "none"
		case "required":
			return "required"
		}
	case map[string]any:
		// {"type": "function", "function": {"name": "foo"}} → required
		if v["type"] == "function" {
			return "required"
		}
	}
	return ""
}

// extractBase64Image extracts base64 data from a data URL.
func extractBase64Image(rawURL string) string {
	if !strings.HasPrefix(rawURL, "data:") {
		return ""
	}
	comma := strings.Index(rawURL, ",")
	if comma < 0 {
		return ""
	}
	return rawURL[comma+1:]
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// Response translation: Ollama response → openai.ChatCompletionResponse
// ---------------------------------------------------------------------------

type ollamaResponse struct {
	Model      string        `json:"model"`
	Message    ollamaRespMsg `json:"message"`
	Done       bool          `json:"done"`
	DoneReason string        `json:"done_reason"`
	EvalCount  int           `json:"eval_count"`
	PromptEval int           `json:"prompt_eval_count"`
	Error      string        `json:"error,omitempty"`
}

type ollamaRespMsg struct {
	Role      string               `json:"role"`
	Content   string               `json:"content"`
	Thinking  string               `json:"thinking,omitempty"`
	ToolCalls []ollamaRespToolCall `json:"tool_calls,omitempty"`
}

type ollamaRespToolCall struct {
	Type     string                 `json:"type"`
	Function ollamaRespToolCallFunc `json:"function"`
}

type ollamaRespToolCallFunc struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func (l *LLM) translateResponse(body []byte, requestModel string) (cogito.LLMReply, cogito.LLMUsage, error) {
	var or ollamaResponse
	if err := json.Unmarshal(body, &or); err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("ollama: parse response: %w", err)
	}

	if or.Error != "" {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("ollama: %s", or.Error)
	}

	content := or.Message.Content
	thinking := or.Message.Thinking

	var toolCalls []openai.ToolCall
	for _, tc := range or.Message.ToolCalls {
		args := string(tc.Function.Arguments)
		if args == "" {
			args = "{}"
		}
		toolCalls = append(toolCalls, openai.ToolCall{
			Type: openai.ToolTypeFunction,
			Function: openai.FunctionCall{
				Name:      tc.Function.Name,
				Arguments: args,
			},
		})
	}

	if content == "" && len(toolCalls) == 0 && thinking == "" {
		return cogito.LLMReply{}, cogito.LLMUsage{}, ErrNoResponse
	}

	finishReason := mapDoneReason(or.DoneReason, toolCalls)

	response := openai.ChatCompletionResponse{
		ID:    "ollama-" + or.Model,
		Model: firstNonEmpty(or.Model, requestModel),
		Choices: []openai.ChatCompletionChoice{{
			Index: 0,
			Message: openai.ChatCompletionMessage{
				Role:             openai.ChatMessageRoleAssistant,
				Content:          content,
				ToolCalls:        toolCalls,
				ReasoningContent: thinking,
			},
			FinishReason: finishReason,
		}},
	}

	usage := cogito.LLMUsage{
		PromptTokens:     or.PromptEval,
		CompletionTokens: or.EvalCount,
		TotalTokens:      or.PromptEval + or.EvalCount,
	}

	return cogito.LLMReply{
		ChatCompletionResponse: response,
		ReasoningContent:       thinking,
	}, usage, nil
}

func mapDoneReason(reason string, toolCalls []openai.ToolCall) openai.FinishReason {
	if len(toolCalls) > 0 {
		return openai.FinishReasonToolCalls
	}
	switch reason {
	case "stop":
		return openai.FinishReasonStop
	case "length":
		return openai.FinishReasonLength
	case "tools":
		return openai.FinishReasonToolCalls
	default:
		return openai.FinishReasonStop
	}
}

func parseAPIError(status int, body []byte) error {
	var or ollamaResponse
	if err := json.Unmarshal(body, &or); err == nil && or.Error != "" {
		return fmt.Errorf("ollama: HTTP %d: %s", status, or.Error)
	}
	return fmt.Errorf("ollama: HTTP %d: %s", status, strings.TrimSpace(string(body)))
}
