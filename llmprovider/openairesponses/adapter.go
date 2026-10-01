// Package openairesponses adapts the OpenAI Responses API to cogito.LLM.
//
// The Responses API (POST /v1/responses) uses an item-based input array
// and an instructions field for system prompts, differing from Chat
// Completions. This adapter follows the same pattern as the Anthropic
// adapter: translate openai.ChatCompletionRequest → Responses API JSON,
// parse the response back into openai types, and wrap in cogito.LLMReply.
package openairesponses

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
	openai "github.com/sashabaranov/go-openai"
)

const defaultMaxTokens = 16384

// Config holds the connection + auth parameters for the Responses adapter.
type Config struct {
	Model           string
	BaseURL         string // e.g. "https://api.openai.com"
	APIKey          string // Bearer token (when IsOAuth is false)
	Token           string // Bearer token (when IsOAuth is true)
	IsOAuth         bool
	ReasoningEffort string
}

// LLM implements cogito.LLM against the OpenAI Responses API.
type LLM struct {
	config Config
	client *http.Client
}

var _ cogito.LLM = (*LLM)(nil)

// New returns a Responses API adapter.
func New(config Config) *LLM {
	if config.BaseURL == "" {
		config.BaseURL = "https://api.openai.com"
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
	fragment.Messages = append(fragment.Messages, reply.ChatCompletionResponse.Choices[0].Message)
	if fragment.Status != nil {
		fragment.Status.LastUsage = usage
		fragment.Status.CumulativeUsage.PromptTokens += usage.PromptTokens
		fragment.Status.CumulativeUsage.CompletionTokens += usage.CompletionTokens
		fragment.Status.CumulativeUsage.TotalTokens += usage.TotalTokens
	}
	return fragment, nil
}

// ErrNoResponse is returned when the API returns a message with no content.
var ErrNoResponse = errors.New("openai-responses: completed without an assistant message")

// CreateChatCompletion translates an OpenAI Chat Completion request to the
// Responses API, calls it, and translates the response back.
func (l *LLM) CreateChatCompletion(ctx context.Context, request openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	body, err := l.translateRequest(request)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}

	url := l.config.BaseURL + "/v1/responses"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("openai-responses: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if l.config.IsOAuth {
		req.Header.Set("Authorization", "Bearer "+l.config.Token)
	} else {
		req.Header.Set("Authorization", "Bearer "+l.config.APIKey)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("openai-responses: request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("openai-responses: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return cogito.LLMReply{}, cogito.LLMUsage{}, parseAPIError(resp.StatusCode, respBody)
	}

	return l.translateResponse(respBody, request.Model)
}

// ---------------------------------------------------------------------------
// Request translation: openai.ChatCompletionRequest → Responses API body
// ---------------------------------------------------------------------------

type responsesRequest struct {
	Model           string              `json:"model"`
	Input           []responsesInput    `json:"input"`
	Instructions    string              `json:"instructions,omitempty"`
	Tools           []responsesTool     `json:"tools,omitempty"`
	ToolChoice      any                 `json:"tool_choice,omitempty"`
	MaxOutputTokens int                 `json:"max_output_tokens,omitempty"`
	Temperature     *float32            `json:"temperature,omitempty"`
	TopP            *float32            `json:"top_p,omitempty"`
	Store           bool                `json:"store"`
	Reasoning       *responsesReasoning `json:"reasoning,omitempty"`
}

type responsesReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type responsesInput struct {
	Type      string `json:"type,omitempty"`
	Role      string `json:"role,omitempty"`
	Content   string `json:"content,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
}

type responsesTool struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters"`
	Strict      bool   `json:"strict"`
}

func (l *LLM) translateRequest(req openai.ChatCompletionRequest) ([]byte, error) {
	rr := responsesRequest{
		Model: firstNonEmpty(req.Model, l.config.Model),
		Store: false,
	}

	var input []responsesInput
	var instructions []string

	for _, msg := range req.Messages {
		switch msg.Role {
		case openai.ChatMessageRoleSystem, openai.ChatMessageRoleDeveloper:
			if msg.Content != "" {
				instructions = append(instructions, msg.Content)
			}
		case openai.ChatMessageRoleUser:
			input = append(input, responsesInput{
				Role:    "user",
				Content: msg.Content,
			})
		case openai.ChatMessageRoleAssistant:
			if msg.Content != "" {
				input = append(input, responsesInput{
					Role:    "assistant",
					Content: msg.Content,
				})
			}
			for _, tc := range msg.ToolCalls {
				input = append(input, responsesInput{
					Type:      "function_call",
					CallID:    tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}
		case openai.ChatMessageRoleTool:
			input = append(input, responsesInput{
				Type:   "function_call_output",
				CallID: msg.ToolCallID,
				Output: msg.Content,
			})
		default:
			input = append(input, responsesInput{
				Role:    "user",
				Content: msg.Content,
			})
		}
	}

	rr.Input = input
	if len(instructions) > 0 {
		rr.Instructions = strings.Join(instructions, "\n\n")
	}

	maxTokens := resolveMaxTokens(req)
	if maxTokens > 0 {
		rr.MaxOutputTokens = maxTokens
	}
	if req.Temperature > 0 {
		t := req.Temperature
		rr.Temperature = &t
	}
	if req.TopP > 0 {
		p := req.TopP
		rr.TopP = &p
	}
	if len(req.Tools) > 0 {
		rr.Tools = translateTools(req.Tools)
	}
	if req.ToolChoice != nil {
		rr.ToolChoice = translateToolChoice(req.ToolChoice)
	}
	effort := firstNonEmpty(l.config.ReasoningEffort, req.ReasoningEffort)
	if effort != "" || supportsReasoningSummary(rr.Model, false) {
		rr.Reasoning = &responsesReasoning{Effort: effort}
		if effort != "none" && supportsReasoningSummary(rr.Model, effort != "") {
			rr.Reasoning.Summary = "auto"
		}
	}

	data, err := json.Marshal(rr)
	if err != nil {
		return nil, fmt.Errorf("openai-responses: marshal request: %w", err)
	}
	return data, nil
}

// supportsReasoningSummary is deliberately narrower than reasoning-effort
// support: o1 and o3-mini accept effort but do not support public summaries.
// The catalog exposes effort choices, not summary support. Unknown deployment
// aliases opt in only through an explicit effort; never guess their capability
// from a provider-wide default.
func supportsReasoningSummary(model string, explicitEffort bool) bool {
	model = strings.ToLower(model)
	if strings.HasPrefix(model, "gpt-5") {
		return !strings.Contains(model, "chat")
	}
	if model == "o3" || strings.HasPrefix(model, "o3-") {
		return !strings.HasPrefix(model, "o3-mini")
	}
	if model == "o4-mini" || strings.HasPrefix(model, "o4-mini-") || model == "codex-mini-latest" {
		return true
	}
	if strings.HasPrefix(model, "gpt-") || model == "o1" || strings.HasPrefix(model, "o1-") {
		return false
	}
	return explicitEffort
}

func translateTools(tools []openai.Tool) []responsesTool {
	out := make([]responsesTool, 0, len(tools))
	for _, t := range tools {
		if t.Function == nil {
			continue
		}
		out = append(out, responsesTool{
			Type:        "function",
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
			Strict:      false,
		})
	}
	return out
}

func translateToolChoice(choice any) any {
	switch v := choice.(type) {
	case string:
		return v
	case map[string]any:
		if fn, ok := v["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok {
				return map[string]string{"type": "function", "name": name}
			}
		}
	}
	return nil
}

func resolveMaxTokens(req openai.ChatCompletionRequest) int {
	if req.MaxCompletionTokens > 0 {
		return req.MaxCompletionTokens
	}
	if req.MaxTokens > 0 {
		return req.MaxTokens
	}
	return 0
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// Response translation: Responses API response → openai.ChatCompletionResponse
// ---------------------------------------------------------------------------

type responsesAPIResponse struct {
	ID     string                `json:"id"`
	Object string                `json:"object"`
	Model  string                `json:"model"`
	Output []responsesOutputItem `json:"output"`
	Usage  responsesAPIUsage     `json:"usage"`
	Status string                `json:"status"`
	Error  *responsesAPIError    `json:"error"`
}

type responsesOutputItem struct {
	Type             string                   `json:"type"`
	Role             string                   `json:"role,omitempty"`
	Content          []responsesOutputContent `json:"content,omitempty"`
	Summary          []responsesOutputContent `json:"summary,omitempty"`
	EncryptedContent string                   `json:"encrypted_content,omitempty"`
	CallID           string                   `json:"call_id,omitempty"`
	Name             string                   `json:"name,omitempty"`
	Arguments        string                   `json:"arguments,omitempty"`
	ID               string                   `json:"id,omitempty"`
}

type responsesOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesAPIUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type responsesAPIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (l *LLM) translateResponse(body []byte, requestModel string) (cogito.LLMReply, cogito.LLMUsage, error) {
	var ar responsesAPIResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("openai-responses: parse response: %w", err)
	}

	if ar.Error != nil && ar.Error.Message != "" {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("openai-responses: API error %s: %s", ar.Error.Code, ar.Error.Message)
	}

	var textParts []string
	var reasoningParts []string
	var toolCalls []openai.ToolCall

	for _, item := range ar.Output {
		switch item.Type {
		case "message":
			if item.Role == "assistant" || item.Role == "" {
				for _, c := range item.Content {
					if c.Type == "output_text" {
						textParts = append(textParts, c.Text)
					}
				}
			}
		case "reasoning":
			for _, part := range item.Summary {
				if part.Type == "summary_text" && strings.TrimSpace(part.Text) != "" {
					reasoningParts = append(reasoningParts, part.Text)
				}
			}
		case "function_call":
			toolCalls = append(toolCalls, openai.ToolCall{
				ID:   item.CallID,
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			})
		}
	}

	content := strings.Join(textParts, "")
	finishReason := openai.FinishReasonStop
	if len(toolCalls) > 0 {
		finishReason = openai.FinishReasonToolCalls
	}
	if content == "" && len(toolCalls) == 0 {
		return cogito.LLMReply{}, cogito.LLMUsage{}, ErrNoResponse
	}

	response := openai.ChatCompletionResponse{
		ID:     ar.ID,
		Object: "chat.completion",
		Model:  firstNonEmpty(ar.Model, requestModel),
		Choices: []openai.ChatCompletionChoice{{
			Index: 0,
			Message: openai.ChatCompletionMessage{
				Role:      openai.ChatMessageRoleAssistant,
				Content:   content,
				ToolCalls: toolCalls,
			},
			FinishReason: finishReason,
		}},
	}

	totalTokens := ar.Usage.TotalTokens
	if totalTokens == 0 {
		totalTokens = ar.Usage.InputTokens + ar.Usage.OutputTokens
	}

	usage := cogito.LLMUsage{
		PromptTokens:     ar.Usage.InputTokens,
		CompletionTokens: ar.Usage.OutputTokens,
		TotalTokens:      totalTokens,
	}

	return cogito.LLMReply{
		ChatCompletionResponse: response,
		ReasoningContent:       strings.Join(reasoningParts, "\n"),
	}, usage, nil
}

func parseAPIError(status int, body []byte) error {
	var errResp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error.Message != "" {
		return fmt.Errorf("openai-responses: %d %s: %s", status, errResp.Error.Code, errResp.Error.Message)
	}
	return fmt.Errorf("openai-responses: HTTP %d: %s", status, strings.TrimSpace(string(body)))
}
