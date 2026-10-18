// Package anthropic adapts the Anthropic Messages API to cogito.LLM.
//
// cogito speaks OpenAI Chat Completions natively; Anthropic does not. This
// adapter follows the codexapp pattern: translate openai.ChatCompletionRequest
// → Anthropic /v1/messages, parse the response back into openai types, and
// wrap in cogito.LLMReply. Both API-key (x-api-key) and OAuth (Bearer +
// anthropic-beta) auth are supported.
package anthropic

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

const (
	// anthropicVersion is the API version header every request carries.
	anthropicVersion = "2023-06-01"
	// oauthBetaHeader enables OAuth bearer-token auth.
	oauthBetaHeader = "oauth-2025-04-20"
	// defaultMaxTokens is used when the request does not set a token cap.
	// Anthropic requires max_tokens; OpenAI requests often omit it.
	defaultMaxTokens = 16384
)

// Config holds the connection + auth parameters for the Anthropic adapter.
type Config struct {
	Model   string
	BaseURL string // default "https://api.anthropic.com"
	APIKey  string // x-api-key auth (when IsOAuth is false)
	Token   string // Bearer auth (when IsOAuth is true)
	IsOAuth bool   // true = OAuth bearer + beta header; false = x-api-key
}

// LLM implements cogito.LLM (and cogito.StreamingLLM) against the Anthropic
// Messages API.
type LLM struct {
	config Config
	client *http.Client
}

var _ cogito.LLM = (*LLM)(nil)

// New returns an Anthropic adapter. If BaseURL is empty, defaults to
// https://api.anthropic.com.
func New(config Config) *LLM {
	if config.BaseURL == "" {
		config.BaseURL = "https://api.anthropic.com"
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
var ErrNoResponse = errors.New("anthropic: completed without an assistant message")

// CreateChatCompletion translates an OpenAI Chat Completion request to the
// Anthropic Messages API, calls it, and translates the response back.
func (l *LLM) CreateChatCompletion(ctx context.Context, request openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	body, err := l.translateRequest(request)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}

	url := l.config.BaseURL + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("anthropic: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	l.setAuthHeaders(req)

	resp, err := l.client.Do(req)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("anthropic: request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("anthropic: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return cogito.LLMReply{}, cogito.LLMUsage{}, parseAPIError(resp.StatusCode, respBody)
	}

	return l.translateResponse(respBody, request.Model)
}

// setAuthHeaders applies the version and credential headers every Messages
// API call carries: x-api-key for API keys, Bearer plus the OAuth beta flag
// for a /login OAuth token.
func (l *LLM) setAuthHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-version", anthropicVersion)
	if l.config.IsOAuth {
		req.Header.Set("Authorization", "Bearer "+l.config.Token)
		req.Header.Set("anthropic-beta", oauthBetaHeader)
	} else {
		req.Header.Set("x-api-key", l.config.APIKey)
	}
}

// ---------------------------------------------------------------------------
// Request translation: openai.ChatCompletionRequest → Anthropic Messages body
// ---------------------------------------------------------------------------

// anthropicRequest is the JSON body for POST /v1/messages.
type anthropicRequest struct {
	Model         string             `json:"model"`
	MaxTokens     int                `json:"max_tokens"`
	System        string             `json:"system,omitempty"`
	Messages      []anthropicMessage `json:"messages"`
	Temperature   *float32           `json:"temperature,omitempty"`
	TopP          *float32           `json:"top_p,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
	Tools         []anthropicTool    `json:"tools,omitempty"`
	ToolChoice    any                `json:"tool_choice,omitempty"`
}

// anthropicMessage is one message in the conversation. Content can be a plain
// string (simple text) or a []map[string]any (content blocks for tool_use,
// tool_result, images).
type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema"`
}

func (l *LLM) translateRequest(req openai.ChatCompletionRequest) ([]byte, error) {
	ar := anthropicRequest{
		Model:     firstNonEmpty(req.Model, l.config.Model),
		MaxTokens: resolveMaxTokens(req),
	}

	var conversation []anthropicMessage
	var systemParts []string

	for _, msg := range req.Messages {
		switch msg.Role {
		case openai.ChatMessageRoleSystem, openai.ChatMessageRoleDeveloper:
			if msg.Content != "" {
				systemParts = append(systemParts, msg.Content)
			}
		case openai.ChatMessageRoleUser:
			conversation = append(conversation, translateUserMessage(msg))
		case openai.ChatMessageRoleAssistant:
			conversation = append(conversation, translateAssistantMessage(msg))
		case openai.ChatMessageRoleTool:
			conversation = append(conversation, translateToolResultMessage(msg))
		default:
			// Unknown role: treat as user to avoid dropping content.
			conversation = append(conversation, anthropicMessage{
				Role:    "user",
				Content: msg.Content,
			})
		}
	}

	if len(systemParts) > 0 {
		ar.System = strings.Join(systemParts, "\n\n")
	}
	ar.Messages = conversation

	if req.Temperature > 0 {
		t := req.Temperature
		ar.Temperature = &t
	}
	if req.TopP > 0 {
		p := req.TopP
		ar.TopP = &p
	}
	if len(req.Stop) > 0 {
		ar.StopSequences = req.Stop
	}
	if len(req.Tools) > 0 {
		ar.Tools = translateTools(req.Tools)
	}
	if req.ToolChoice != nil {
		ar.ToolChoice = translateToolChoice(req.ToolChoice)
	}

	data, err := json.Marshal(ar)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}
	return data, nil
}

// translateUserMessage handles user messages, including multi-content (images).
func translateUserMessage(msg openai.ChatCompletionMessage) anthropicMessage {
	if len(msg.MultiContent) == 0 {
		return anthropicMessage{Role: "user", Content: msg.Content}
	}
	blocks := make([]map[string]any, 0, len(msg.MultiContent))
	for _, part := range msg.MultiContent {
		switch part.Type {
		case openai.ChatMessagePartTypeText:
			blocks = append(blocks, map[string]any{"type": "text", "text": part.Text})
		case openai.ChatMessagePartTypeImageURL:
			if img := parseImageURL(part.ImageURL.URL); img != nil {
				blocks = append(blocks, img)
			}
		}
	}
	if len(blocks) == 0 {
		return anthropicMessage{Role: "user", Content: msg.Content}
	}
	return anthropicMessage{Role: "user", Content: blocks}
}

// translateAssistantMessage handles assistant messages, including tool calls.
func translateAssistantMessage(msg openai.ChatCompletionMessage) anthropicMessage {
	if len(msg.ToolCalls) == 0 {
		return anthropicMessage{Role: "assistant", Content: msg.Content}
	}
	blocks := make([]map[string]any, 0, len(msg.ToolCalls)+1)
	if msg.Content != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": msg.Content})
	}
	for _, tc := range msg.ToolCalls {
		var input any
		if tc.Function.Arguments != "" {
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
		}
		blocks = append(blocks, map[string]any{
			"type":  "tool_use",
			"id":    tc.ID,
			"name":  tc.Function.Name,
			"input": input,
		})
	}
	return anthropicMessage{Role: "assistant", Content: blocks}
}

// translateToolResultMessage converts an OpenAI tool-result message into a
// user message with a tool_result content block (Anthropic's convention).
func translateToolResultMessage(msg openai.ChatCompletionMessage) anthropicMessage {
	block := map[string]any{
		"type":        "tool_result",
		"tool_use_id": msg.ToolCallID,
	}
	if msg.Content != "" {
		block["content"] = msg.Content
	}
	return anthropicMessage{
		Role:    "user",
		Content: []map[string]any{block},
	}
}

func translateTools(tools []openai.Tool) []anthropicTool {
	out := make([]anthropicTool, 0, len(tools))
	for _, t := range tools {
		if t.Function == nil {
			continue
		}
		out = append(out, anthropicTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}
	return out
}

func translateToolChoice(choice any) any {
	switch v := choice.(type) {
	case string:
		switch v {
		case "auto":
			return map[string]string{"type": "auto"}
		case "none":
			return map[string]string{"type": "none"}
		case "required":
			return map[string]string{"type": "any"}
		}
	case map[string]any:
		// {"type": "function", "function": {"name": "foo"}}
		if fn, ok := v["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok {
				return map[string]string{"type": "tool", "name": name}
			}
		}
	}
	return map[string]string{"type": "auto"}
}

// parseImageURL extracts base64 image data from a data URL. Returns an
// Anthropic image content block or nil if the URL is not a data URL.
func parseImageURL(rawURL string) map[string]any {
	if !strings.HasPrefix(rawURL, "data:") {
		return nil
	}
	// Format: data:{media_type};base64,{data}
	semi := strings.Index(rawURL, ";")
	comma := strings.Index(rawURL, ",")
	if semi < 0 || comma < 0 || semi > comma {
		return nil
	}
	mediaType := rawURL[len("data:"):semi]
	data := rawURL[comma+1:]
	return map[string]any{
		"type": "image",
		"source": map[string]any{
			"type":       "base64",
			"media_type": mediaType,
			"data":       data,
		},
	}
}

func resolveMaxTokens(req openai.ChatCompletionRequest) int {
	if req.MaxCompletionTokens > 0 {
		return req.MaxCompletionTokens
	}
	if req.MaxTokens > 0 {
		return req.MaxTokens
	}
	return defaultMaxTokens
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// Response translation: Anthropic response → openai.ChatCompletionResponse
// ---------------------------------------------------------------------------

type anthropicResponse struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Role       string                  `json:"role"`
	Content    []anthropicContentBlock `json:"content"`
	Model      string                  `json:"model"`
	StopReason string                  `json:"stop_reason"`
	Usage      anthropicUsage          `json:"usage"`
}

type anthropicContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicErrorResp struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (l *LLM) translateResponse(body []byte, requestModel string) (cogito.LLMReply, cogito.LLMUsage, error) {
	var ar anthropicResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("anthropic: parse response: %w", err)
	}

	var textParts []string
	var toolCalls []openai.ToolCall
	var thinking string

	for _, block := range ar.Content {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "tool_use":
			args := string(block.Input)
			if args == "" {
				args = "{}"
			}
			toolCalls = append(toolCalls, openai.ToolCall{
				ID:   block.ID,
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      block.Name,
					Arguments: args,
				},
			})
		case "thinking":
			thinking += block.Text
		}
	}

	finishReason := mapStopReason(ar.StopReason)
	content := strings.Join(textParts, "")
	if content == "" && len(toolCalls) == 0 {
		return cogito.LLMReply{}, cogito.LLMUsage{}, ErrNoResponse
	}

	response := openai.ChatCompletionResponse{
		ID:    ar.ID,
		Model: firstNonEmpty(ar.Model, requestModel),
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
		PromptTokens:     ar.Usage.InputTokens,
		CompletionTokens: ar.Usage.OutputTokens,
		TotalTokens:      ar.Usage.InputTokens + ar.Usage.OutputTokens,
	}

	return cogito.LLMReply{
		ChatCompletionResponse: response,
		ReasoningContent:       thinking,
	}, usage, nil
}

func mapStopReason(reason string) openai.FinishReason {
	switch reason {
	case "end_turn", "stop_sequence":
		return openai.FinishReasonStop
	case "tool_use":
		return openai.FinishReasonToolCalls
	case "max_tokens":
		return openai.FinishReasonLength
	default:
		return openai.FinishReasonStop
	}
}

func parseAPIError(status int, body []byte) error {
	var ae anthropicErrorResp
	if err := json.Unmarshal(body, &ae); err == nil && ae.Error.Message != "" {
		return fmt.Errorf("anthropic: %d %s: %s", status, ae.Error.Type, ae.Error.Message)
	}
	return fmt.Errorf("anthropic: HTTP %d: %s", status, strings.TrimSpace(string(body)))
}
