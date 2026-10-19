// Package google adapts the Google Generative AI API to cogito.LLM.
//
// cogito speaks OpenAI Chat Completions natively; Google Gemini does not. This
// adapter follows the same pattern as the Anthropic adapter: translate
// openai.ChatCompletionRequest → Gemini generateContent, parse the response
// back into openai types, and wrap in cogito.LLMReply. Both API-key
// (x-goog-api-key) and OAuth (Bearer) auth are supported.
package google

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
	// defaultMaxTokens is used when the request does not set a token cap.
	// Gemini requires maxOutputTokens to be meaningful; OpenAI requests often omit it.
	defaultMaxTokens = 16384
)

// Config holds the connection + auth parameters for the Google Gemini adapter.
type Config struct {
	Model   string
	BaseURL string // default "https://generativelanguage.googleapis.com/v1beta"
	APIKey  string // x-goog-api-key auth (when IsOAuth is false)
	Token   string // Bearer auth (when IsOAuth is true)
	IsOAuth bool   // true = OAuth bearer; false = x-goog-api-key
}

// LLM implements cogito.LLM against the Google Generative AI API.
type LLM struct {
	config Config
	client *http.Client
}

var _ cogito.LLM = (*LLM)(nil)

// New returns a Google Gemini adapter. If BaseURL is empty, defaults to
// https://generativelanguage.googleapis.com/v1beta.
func New(config Config) *LLM {
	if config.BaseURL == "" {
		config.BaseURL = "https://generativelanguage.googleapis.com/v1beta"
	}
	return &LLM{
		config: config,
		client: &http.Client{},
	}
}

// Ask delegates to CreateChatCompletion, mirroring the anthropic/codexapp pattern.
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

// ErrNoResponse is returned when the API returns a candidate with no content.
var ErrNoResponse = errors.New("google: completed without an assistant message")

// CreateChatCompletion translates an OpenAI Chat Completion request to the
// Gemini generateContent API, calls it, and translates the response back.
func (l *LLM) CreateChatCompletion(ctx context.Context, request openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	body, err := l.translateRequest(request)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}

	model := firstNonEmpty(request.Model, l.config.Model)
	url := l.config.BaseURL + "/models/" + model + ":generateContent"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("google: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if l.config.IsOAuth {
		req.Header.Set("Authorization", "Bearer "+l.config.Token)
	} else {
		req.Header.Set("x-goog-api-key", l.config.APIKey)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("google: request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("google: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return cogito.LLMReply{}, cogito.LLMUsage{}, parseAPIError(resp.StatusCode, respBody)
	}

	return l.translateResponse(respBody, model)
}

// ---------------------------------------------------------------------------
// Request translation: openai.ChatCompletionRequest → Gemini generateContent body
// ---------------------------------------------------------------------------

// geminiRequest is the JSON body for POST /models/{model}:generateContent.
type geminiRequest struct {
	Contents          []geminiContent         `json:"contents"`
	SystemInstruction *geminiSystemInstr      `json:"systemInstruction,omitempty"`
	Tools             []geminiToolDecl        `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
	InlineData       *geminiInlineData       `json:"inlineData,omitempty"`
}

type geminiFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiSystemInstr struct {
	Parts []geminiPart `json:"parts"`
}

type geminiToolDecl struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

type geminiFunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type geminiToolConfig struct {
	FunctionCallingConfig *geminiFunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

type geminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode,omitempty"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature     *float32 `json:"temperature,omitempty"`
	TopP            *float32 `json:"topP,omitempty"`
	TopK            *float32 `json:"topK,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

func (l *LLM) translateRequest(req openai.ChatCompletionRequest) ([]byte, error) {
	gr := geminiRequest{}
	var systemParts []string
	var contents []geminiContent

	for _, msg := range req.Messages {
		switch msg.Role {
		case openai.ChatMessageRoleSystem, openai.ChatMessageRoleDeveloper:
			if msg.Content != "" {
				systemParts = append(systemParts, msg.Content)
			}
		case openai.ChatMessageRoleUser:
			contents = append(contents, translateUserMessage(msg))
		case openai.ChatMessageRoleAssistant:
			contents = append(contents, translateAssistantMessage(msg))
		case openai.ChatMessageRoleTool:
			contents = append(contents, translateToolResultMessage(msg))
		default:
			contents = append(contents, geminiContent{
				Role:  "user",
				Parts: []geminiPart{{Text: msg.Content}},
			})
		}
	}

	if len(systemParts) > 0 {
		parts := make([]geminiPart, len(systemParts))
		for i, s := range systemParts {
			parts[i] = geminiPart{Text: s}
		}
		gr.SystemInstruction = &geminiSystemInstr{Parts: parts}
	}
	gr.Contents = contents

	// Generation config
	gc := &geminiGenerationConfig{}
	hasConfig := false
	if req.Temperature > 0 {
		t := req.Temperature
		gc.Temperature = &t
		hasConfig = true
	}
	if req.TopP > 0 {
		p := req.TopP
		gc.TopP = &p
		hasConfig = true
	}
	maxTok := resolveMaxTokens(req)
	if maxTok > 0 {
		gc.MaxOutputTokens = &maxTok
		hasConfig = true
	}
	if len(req.Stop) > 0 {
		gc.StopSequences = req.Stop
		hasConfig = true
	}
	if hasConfig {
		gr.GenerationConfig = gc
	}

	// Tools
	if len(req.Tools) > 0 {
		decls := make([]geminiFunctionDeclaration, 0, len(req.Tools))
		for _, t := range req.Tools {
			if t.Function == nil {
				continue
			}
			var params json.RawMessage
			if t.Function.Parameters != nil {
				params, _ = json.Marshal(t.Function.Parameters)
			}
			decls = append(decls, geminiFunctionDeclaration{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  params,
			})
		}
		if len(decls) > 0 {
			gr.Tools = []geminiToolDecl{{FunctionDeclarations: decls}}
		}
	}

	// Tool choice
	if req.ToolChoice != nil {
		if tc := translateToolChoice(req.ToolChoice); tc != nil {
			gr.ToolConfig = tc
		}
	}

	data, err := json.Marshal(gr)
	if err != nil {
		return nil, fmt.Errorf("google: marshal request: %w", err)
	}
	return data, nil
}

func translateUserMessage(msg openai.ChatCompletionMessage) geminiContent {
	if len(msg.MultiContent) == 0 {
		return geminiContent{
			Role:  "user",
			Parts: []geminiPart{{Text: msg.Content}},
		}
	}
	parts := make([]geminiPart, 0, len(msg.MultiContent))
	for _, part := range msg.MultiContent {
		switch part.Type {
		case openai.ChatMessagePartTypeText:
			parts = append(parts, geminiPart{Text: part.Text})
		case openai.ChatMessagePartTypeImageURL:
			if img := parseImageURL(part.ImageURL.URL); img != nil {
				parts = append(parts, *img)
			}
		}
	}
	if len(parts) == 0 {
		return geminiContent{
			Role:  "user",
			Parts: []geminiPart{{Text: msg.Content}},
		}
	}
	return geminiContent{Role: "user", Parts: parts}
}

func translateAssistantMessage(msg openai.ChatCompletionMessage) geminiContent {
	parts := make([]geminiPart, 0, len(msg.ToolCalls)+1)
	if msg.Content != "" {
		parts = append(parts, geminiPart{Text: msg.Content})
	}
	for _, tc := range msg.ToolCalls {
		var args json.RawMessage
		if tc.Function.Arguments != "" {
			args = json.RawMessage(tc.Function.Arguments)
		} else {
			args = json.RawMessage("{}")
		}
		parts = append(parts, geminiPart{
			FunctionCall: &geminiFunctionCall{
				Name: tc.Function.Name,
				Args: args,
			},
		})
	}
	return geminiContent{Role: "model", Parts: parts}
}

func translateToolResultMessage(msg openai.ChatCompletionMessage) geminiContent {
	var resp json.RawMessage
	if msg.Content != "" {
		resp = json.RawMessage(msg.Content)
	} else {
		resp = json.RawMessage("{}")
	}
	return geminiContent{
		Role: "user",
		Parts: []geminiPart{{
			FunctionResponse: &geminiFunctionResponse{
				Name:     msg.ToolCallID,
				Response: resp,
			},
		}},
	}
}

func translateToolChoice(choice any) *geminiToolConfig {
	switch v := choice.(type) {
	case string:
		switch v {
		case "auto":
			return nil // AUTO is the default; omit to keep the body clean
		case "none":
			return &geminiToolConfig{FunctionCallingConfig: &geminiFunctionCallingConfig{Mode: "NONE"}}
		case "required":
			return &geminiToolConfig{FunctionCallingConfig: &geminiFunctionCallingConfig{Mode: "ANY"}}
		}
	case map[string]any:
		if fn, ok := v["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok {
				return &geminiToolConfig{FunctionCallingConfig: &geminiFunctionCallingConfig{
					Mode:                 "ANY",
					AllowedFunctionNames: []string{name},
				}}
			}
		}
	}
	return nil
}

// parseImageURL extracts base64 image data from a data URL and returns a
// Gemini inlineData part, or nil if the URL is not a data URL.
func parseImageURL(rawURL string) *geminiPart {
	if !strings.HasPrefix(rawURL, "data:") {
		return nil
	}
	// Format: data:{media_type};base64,{data}
	semi := strings.Index(rawURL, ";")
	comma := strings.Index(rawURL, ",")
	if semi < 0 || comma < 0 || semi > comma {
		return nil
	}
	mimeType := rawURL[5:semi] // skip "data:"
	data := rawURL[comma+1:]
	if mimeType == "" || data == "" {
		return nil
	}
	return &geminiPart{
		InlineData: &geminiInlineData{
			MimeType: mimeType,
			Data:     data,
		},
	}
}

// ---------------------------------------------------------------------------
// Response translation: Gemini generateContent → openai.ChatCompletionResponse
// ---------------------------------------------------------------------------

type geminiResponse struct {
	Candidates    []geminiCandidate   `json:"candidates"`
	UsageMetadata geminiUsageMetadata `json:"usageMetadata"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
	Index        int           `json:"index"`
}

type geminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type geminiErrorResp struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

func (l *LLM) translateResponse(body []byte, requestModel string) (cogito.LLMReply, cogito.LLMUsage, error) {
	var gr geminiResponse
	if err := json.Unmarshal(body, &gr); err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("google: parse response: %w", err)
	}

	if len(gr.Candidates) == 0 {
		return cogito.LLMReply{}, cogito.LLMUsage{}, ErrNoResponse
	}

	var textParts []string
	var toolCalls []openai.ToolCall

	for _, part := range gr.Candidates[0].Content.Parts {
		if part.Text != "" {
			textParts = append(textParts, part.Text)
		}
		if part.FunctionCall != nil {
			args := string(part.FunctionCall.Args)
			if args == "" {
				args = "{}"
			}
			toolCalls = append(toolCalls, openai.ToolCall{
				ID:   part.FunctionCall.Name, // Gemini doesn't return a separate call ID
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      part.FunctionCall.Name,
					Arguments: args,
				},
			})
		}
	}

	content := strings.Join(textParts, "")
	if content == "" && len(toolCalls) == 0 {
		return cogito.LLMReply{}, cogito.LLMUsage{}, ErrNoResponse
	}

	finishReason := mapFinishReason(gr.Candidates[0].FinishReason)
	response := openai.ChatCompletionResponse{
		Model: requestModel,
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

	usage := cogito.LLMUsage{
		PromptTokens:     gr.UsageMetadata.PromptTokenCount,
		CompletionTokens: gr.UsageMetadata.CandidatesTokenCount,
		TotalTokens:      gr.UsageMetadata.TotalTokenCount,
	}

	return cogito.LLMReply{
		ChatCompletionResponse: response,
	}, usage, nil
}

func mapFinishReason(reason string) openai.FinishReason {
	switch reason {
	case "STOP", "MAX_TOKENS":
		if reason == "MAX_TOKENS" {
			return openai.FinishReasonLength
		}
		return openai.FinishReasonStop
	case "SAFETY":
		return openai.FinishReasonContentFilter
	case "RECITATION":
		return openai.FinishReasonContentFilter
	default:
		return openai.FinishReasonStop
	}
}

func parseAPIError(status int, body []byte) error {
	var ge geminiErrorResp
	if err := json.Unmarshal(body, &ge); err == nil && ge.Error.Message != "" {
		return fmt.Errorf("google: %d %s: %s", status, ge.Error.Status, ge.Error.Message)
	}
	return fmt.Errorf("google: HTTP %d: %s", status, strings.TrimSpace(string(body)))
}

func resolveMaxTokens(req openai.ChatCompletionRequest) int {
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
