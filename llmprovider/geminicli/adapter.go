// Package geminicli adapts the Google Cloud Code Assist API (used by Gemini
// CLI) to cogito.LLM.
//
// Cloud Code Assist uses the same Gemini generateContent body format but
// wraps it in an envelope {project, model, request: {...}} and posts to
// https://cloudcode-pa.googleapis.com/v1internal:generateContent. Auth is
// OAuth-based: the credential is a JSON blob carrying access_token +
// project_id (+ optional refresh_token / expires_at).
//
// This adapter follows the same pattern as the google (AI Studio) adapter:
// translate openai.ChatCompletionRequest → Cloud Code Assist, parse the
// response back into openai types.
package geminicli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/internal/fragmentreply"
	openai "github.com/sashabaranov/go-openai"
)

const (
	// defaultEndpoint is the Cloud Code Assist API root.
	defaultEndpoint = "https://cloudcode-pa.googleapis.com"
	// cliUserAgent identifies as Gemini CLI to unlock higher rate limits.
	cliUserAgent = "GeminiCLI/0.46.0/gemini-2.5-pro (linux; amd64; terminal)"
	// clientMetadata is the required Client-Metadata header value.
	clientMetadata = "ideType=IDE_UNSPECIFIED,platform=PLATFORM_UNSPECIFIED,pluginType=GEMINI"
)

// Config holds the connection + auth parameters for the Cloud Code Assist adapter.
type Config struct {
	Model      string
	BaseURL    string // default "https://cloudcode-pa.googleapis.com"
	Credential string // JSON blob: {token, projectId, refreshToken?, expiresAt?}
}

// LLM implements cogito.LLM against the Cloud Code Assist API.
type LLM struct {
	config Config
	client *http.Client
}

var _ cogito.LLM = (*LLM)(nil)

// New returns a Cloud Code Assist adapter. If BaseURL is empty, defaults to
// https://cloudcode-pa.googleapis.com.
func New(config Config) *LLM {
	if config.BaseURL == "" {
		config.BaseURL = defaultEndpoint
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
var ErrNoResponse = errors.New("gemini-cli: completed without an assistant message")

// CreateChatCompletion translates an OpenAI Chat Completion request to the
// Cloud Code Assist API, calls it, and translates the response back.
func (l *LLM) CreateChatCompletion(ctx context.Context, request openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	creds, err := parseCredentials(l.config.Credential)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("gemini-cli: %w", err)
	}

	body, err := l.translateRequest(request, creds.ProjectID)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}

	url := strings.TrimRight(l.config.BaseURL, "/") + "/v1internal:generateContent"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("gemini-cli: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	req.Header.Set("User-Agent", cliUserAgent)
	req.Header.Set("Client-Metadata", clientMetadata)

	resp, err := l.client.Do(req)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("gemini-cli: request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("gemini-cli: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return cogito.LLMReply{}, cogito.LLMUsage{}, parseAPIError(resp.StatusCode, respBody)
	}

	return l.translateResponse(respBody, firstNonEmpty(request.Model, l.config.Model))
}

// ---------------------------------------------------------------------------
// Credential parsing
// ---------------------------------------------------------------------------

type credentials struct {
	AccessToken  string
	ProjectID    string
	RefreshToken string
	ExpiresAt    time.Time
	Email        string
}

type rawCredential struct {
	Token           string      `json:"token"`
	AccessToken     string      `json:"access_token"`
	ProjectID       string      `json:"projectId"`
	ProjectIDAlt    string      `json:"project_id"`
	RefreshToken    string      `json:"refreshToken"`
	RefreshTokenAlt string      `json:"refresh_token"`
	Refresh         string      `json:"refresh"`
	ExpiresAt       json.Number `json:"expiresAt"`
	ExpiresAtAlt    json.Number `json:"expires_at"`
	Expires         json.Number `json:"expires"`
	Email           string      `json:"email"`
}

func parseCredentials(raw string) (*credentials, error) {
	if raw == "" {
		return nil, errors.New("no credentials — run 'nib login google-gemini-cli'")
	}
	var rc rawCredential
	if err := json.Unmarshal([]byte(raw), &rc); err != nil {
		return nil, fmt.Errorf("invalid credentials (not JSON): %w", err)
	}
	token := firstNonEmpty(rc.Token, rc.AccessToken)
	projectID := firstNonEmpty(rc.ProjectID, rc.ProjectIDAlt)
	if token == "" || projectID == "" {
		return nil, errors.New("missing token or projectId in credentials")
	}
	creds := &credentials{
		AccessToken:  token,
		ProjectID:    projectID,
		RefreshToken: firstNonEmpty(rc.RefreshToken, rc.RefreshTokenAlt, rc.Refresh),
		Email:        rc.Email,
	}
	// Parse expiry — could be seconds or milliseconds
	for _, exp := range []json.Number{rc.ExpiresAt, rc.ExpiresAtAlt, rc.Expires} {
		if exp == "" {
			continue
		}
		if ms, err := exp.Int64(); err == nil {
			if ms < 10_000_000_000 {
				creds.ExpiresAt = time.Unix(ms, 0)
			} else {
				creds.ExpiresAt = time.UnixMilli(ms)
			}
			break
		}
	}
	return creds, nil
}

// ---------------------------------------------------------------------------
// Request translation: openai.ChatCompletionRequest → Cloud Code Assist body
// ---------------------------------------------------------------------------

// ccaRequest is the Cloud Code Assist envelope.
type ccaRequest struct {
	Project string   `json:"project"`
	Model   string   `json:"model"`
	Request ccaInner `json:"request"`
}

type ccaInner struct {
	Contents          []ccaContent         `json:"contents"`
	SystemInstruction *ccaSystemInstr      `json:"systemInstruction,omitempty"`
	Tools             []ccaToolDecl        `json:"tools,omitempty"`
	ToolConfig        *ccaToolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *ccaGenerationConfig `json:"generationConfig,omitempty"`
}

type ccaContent struct {
	Role  string    `json:"role"`
	Parts []ccaPart `json:"parts"`
}

type ccaPart struct {
	Text             string               `json:"text,omitempty"`
	FunctionCall     *ccaFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *ccaFunctionResponse `json:"functionResponse,omitempty"`
	InlineData       *ccaInlineData       `json:"inlineData,omitempty"`
}

type ccaFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type ccaFunctionResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

type ccaInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type ccaSystemInstr struct {
	Parts []ccaPart `json:"parts"`
}

type ccaToolDecl struct {
	FunctionDeclarations []ccaFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

type ccaFunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type ccaToolConfig struct {
	FunctionCallingConfig *ccaFunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

type ccaFunctionCallingConfig struct {
	Mode                 string   `json:"mode,omitempty"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

type ccaGenerationConfig struct {
	Temperature     *float32 `json:"temperature,omitempty"`
	TopP            *float32 `json:"topP,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
}

func (l *LLM) translateRequest(req openai.ChatCompletionRequest, projectID string) ([]byte, error) {
	inner := ccaInner{}
	var systemParts []string
	var contents []ccaContent

	// Build tool-call-ID → name map for tool result messages.
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
				systemParts = append(systemParts, msg.Content)
			}
		case openai.ChatMessageRoleUser:
			contents = append(contents, translateCCAUserMessage(msg))
		case openai.ChatMessageRoleAssistant:
			contents = append(contents, translateCCAAssistantMessage(msg))
		case openai.ChatMessageRoleTool:
			name := toolCallNames[msg.ToolCallID]
			contents = append(contents, ccaContent{
				Role: "user",
				Parts: []ccaPart{{
					FunctionResponse: &ccaFunctionResponse{
						Name:     name,
						Response: json.RawMessage(orDefault(msg.Content, "{}")),
					},
				}},
			})
		default:
			contents = append(contents, ccaContent{
				Role: "user", Parts: []ccaPart{{Text: msg.Content}},
			})
		}
	}

	if len(systemParts) > 0 {
		parts := make([]ccaPart, len(systemParts))
		for i, s := range systemParts {
			parts[i] = ccaPart{Text: s}
		}
		inner.SystemInstruction = &ccaSystemInstr{Parts: parts}
	}
	inner.Contents = contents

	// Generation config
	gc := &ccaGenerationConfig{}
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
	if hasConfig {
		inner.GenerationConfig = gc
	}

	// Tools
	if len(req.Tools) > 0 {
		decls := make([]ccaFunctionDeclaration, 0, len(req.Tools))
		for _, t := range req.Tools {
			if t.Function == nil {
				continue
			}
			var params json.RawMessage
			if t.Function.Parameters != nil {
				params, _ = json.Marshal(t.Function.Parameters)
			}
			decls = append(decls, ccaFunctionDeclaration{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  params,
			})
		}
		if len(decls) > 0 {
			inner.Tools = []ccaToolDecl{{FunctionDeclarations: decls}}
		}
	}

	// Tool choice
	if req.ToolChoice != nil {
		if tc := translateCCAToolChoice(req.ToolChoice); tc != nil {
			inner.ToolConfig = tc
		}
	}

	envelope := ccaRequest{
		Project: projectID,
		Model:   firstNonEmpty(req.Model, l.config.Model),
		Request: inner,
	}

	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("gemini-cli: marshal request: %w", err)
	}
	return data, nil
}

func translateCCAUserMessage(msg openai.ChatCompletionMessage) ccaContent {
	if len(msg.MultiContent) == 0 {
		return ccaContent{Role: "user", Parts: []ccaPart{{Text: msg.Content}}}
	}
	parts := make([]ccaPart, 0, len(msg.MultiContent))
	for _, part := range msg.MultiContent {
		switch part.Type {
		case openai.ChatMessagePartTypeText:
			parts = append(parts, ccaPart{Text: part.Text})
		case openai.ChatMessagePartTypeImageURL:
			if img := parseImageURL(part.ImageURL.URL); img != nil {
				parts = append(parts, *img)
			}
		}
	}
	if len(parts) == 0 {
		return ccaContent{Role: "user", Parts: []ccaPart{{Text: msg.Content}}}
	}
	return ccaContent{Role: "user", Parts: parts}
}

func translateCCAAssistantMessage(msg openai.ChatCompletionMessage) ccaContent {
	parts := make([]ccaPart, 0, len(msg.ToolCalls)+1)
	if msg.Content != "" {
		parts = append(parts, ccaPart{Text: msg.Content})
	}
	for _, tc := range msg.ToolCalls {
		var args json.RawMessage
		if tc.Function.Arguments != "" {
			args = json.RawMessage(tc.Function.Arguments)
		} else {
			args = json.RawMessage("{}")
		}
		parts = append(parts, ccaPart{
			FunctionCall: &ccaFunctionCall{
				Name: tc.Function.Name,
				Args: args,
			},
		})
	}
	return ccaContent{Role: "model", Parts: parts}
}

func translateCCAToolChoice(choice any) *ccaToolConfig {
	switch v := choice.(type) {
	case string:
		switch v {
		case "auto":
			return nil
		case "none":
			return &ccaToolConfig{FunctionCallingConfig: &ccaFunctionCallingConfig{Mode: "NONE"}}
		case "required":
			return &ccaToolConfig{FunctionCallingConfig: &ccaFunctionCallingConfig{Mode: "ANY"}}
		}
	case map[string]any:
		if fn, ok := v["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok {
				return &ccaToolConfig{FunctionCallingConfig: &ccaFunctionCallingConfig{
					Mode:                 "ANY",
					AllowedFunctionNames: []string{name},
				}}
			}
		}
	}
	return nil
}

func parseImageURL(rawURL string) *ccaPart {
	if !strings.HasPrefix(rawURL, "data:") {
		return nil
	}
	semi := strings.Index(rawURL, ";")
	comma := strings.Index(rawURL, ",")
	if semi < 0 || comma < 0 || semi > comma {
		return nil
	}
	mimeType := rawURL[5:semi]
	data := rawURL[comma+1:]
	if mimeType == "" || data == "" {
		return nil
	}
	return &ccaPart{
		InlineData: &ccaInlineData{MimeType: mimeType, Data: data},
	}
}

// ---------------------------------------------------------------------------
// Response translation: Cloud Code Assist → openai.ChatCompletionResponse
// ---------------------------------------------------------------------------

type ccaResponse struct {
	Response ccaResponseBody `json:"response"`
	Error    *ccaError       `json:"error,omitempty"`
}

type ccaResponseBody struct {
	Candidates    []ccaCandidate   `json:"candidates"`
	UsageMetadata ccaUsageMetadata `json:"usageMetadata"`
	ModelVersion  string           `json:"modelVersion"`
}

type ccaCandidate struct {
	Content      ccaContent `json:"content"`
	FinishReason string     `json:"finishReason"`
	Index        int        `json:"index"`
}

type ccaUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type ccaError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

func (l *LLM) translateResponse(body []byte, requestModel string) (cogito.LLMReply, cogito.LLMUsage, error) {
	var cr ccaResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("gemini-cli: parse response: %w", err)
	}
	if cr.Error != nil && cr.Error.Message != "" {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("gemini-cli: %d %s: %s", cr.Error.Code, cr.Error.Status, cr.Error.Message)
	}
	if len(cr.Response.Candidates) == 0 {
		return cogito.LLMReply{}, cogito.LLMUsage{}, ErrNoResponse
	}

	var textParts []string
	var toolCalls []openai.ToolCall

	for _, part := range cr.Response.Candidates[0].Content.Parts {
		if part.Text != "" {
			textParts = append(textParts, part.Text)
		}
		if part.FunctionCall != nil {
			args := string(part.FunctionCall.Args)
			if args == "" {
				args = "{}"
			}
			toolCalls = append(toolCalls, openai.ToolCall{
				ID:   part.FunctionCall.Name,
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

	finishReason := mapFinishReason(cr.Response.Candidates[0].FinishReason)
	response := openai.ChatCompletionResponse{
		Model: firstNonEmpty(cr.Response.ModelVersion, requestModel),
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
		PromptTokens:     cr.Response.UsageMetadata.PromptTokenCount,
		CompletionTokens: cr.Response.UsageMetadata.CandidatesTokenCount,
		TotalTokens:      cr.Response.UsageMetadata.TotalTokenCount,
	}

	return cogito.LLMReply{
		ChatCompletionResponse: response,
	}, usage, nil
}

func mapFinishReason(reason string) openai.FinishReason {
	switch reason {
	case "STOP":
		return openai.FinishReasonStop
	case "MAX_TOKENS":
		return openai.FinishReasonLength
	case "SAFETY", "RECITATION":
		return openai.FinishReasonContentFilter
	default:
		return openai.FinishReasonStop
	}
}

func parseAPIError(status int, body []byte) error {
	var cr ccaResponse
	if err := json.Unmarshal(body, &cr); err == nil && cr.Error != nil && cr.Error.Message != "" {
		return fmt.Errorf("gemini-cli: %d %s: %s", status, cr.Error.Status, cr.Error.Message)
	}
	return fmt.Errorf("gemini-cli: HTTP %d: %s", status, strings.TrimSpace(string(body)))
}

func resolveMaxTokens(req openai.ChatCompletionRequest) int {
	if req.MaxTokens > 0 {
		return req.MaxTokens
	}
	if req.MaxCompletionTokens > 0 {
		return req.MaxCompletionTokens
	}
	return 0
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func orDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
