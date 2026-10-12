// Package copilot implements the GitHub Copilot multi-protocol adapter.
//
// Copilot does not have its own wire protocol — it proxies to OpenAI Chat
// Completions, OpenAI Responses, or Anthropic Messages depending on the model.
// The adapter discovers the API endpoint via GraphQL, resolves a token from
// the gh CLI / Copilot client config, and routes each request to the right
// protocol using advertised capabilities, with model-name fallback.
//
// Token resolution (env → ~/.config/github-copilot/ → ~/.config/gh/) lives in
// discovery.go. Protocol translation reuses the anthropic and openairesponses
// packages.
package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/llmprovider/anthropic"
	"github.com/mudler/nib/llmprovider/openairesponses"
	openai "github.com/sashabaranov/go-openai"
)

const (
	editorVersion = "nib/0.1.0"
)

var _ cogito.LLM = (*LLM)(nil)

// Config holds the Copilot adapter configuration.
type Config struct {
	Model string
	Token string
}

// LLM implements cogito.LLM for GitHub Copilot.
type LLM struct {
	config   Config
	client   *http.Client
	endpoint string

	mu                              sync.Mutex
	resolved                        bool
	metadataMu                      sync.Mutex
	metadataEndpoint, metadataToken string
	metadataExpires                 time.Time
	metadata                        map[string]modelCapabilities
}

// New creates a Copilot adapter. The API endpoint is discovered lazily on the
// first request.
func New(config Config) *LLM {
	return &LLM{
		config: config,
		client: &http.Client{},
	}
}

// Ask delegates to CreateChatCompletion, mirroring the other adapters.
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
var ErrNoResponse = fmt.Errorf("copilot: completed without an assistant message")

// CreateChatCompletion routes the request to the appropriate Copilot API
// based on advertised capabilities, then translates the response back to OpenAI
// types.
func (l *LLM) CreateChatCompletion(ctx context.Context, request openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	endpoint, err := l.ensureEndpoint(ctx)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}

	model := request.Model
	if model == "" {
		model = l.config.Model
	}

	request.Model = model
	metadata, err := l.modelMetadata(ctx, endpoint, model)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}
	route, err := metadata.route(model)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}
	switch route {
	case routeMessages:
		return l.doMessages(ctx, endpoint, request)
	case routeResponses:
		return l.doResponses(ctx, endpoint, request, metadata.Capabilities.Supports.ReasoningSummary)
	default:
		return l.doChatCompletions(ctx, endpoint, request)
	}
}

type protocolRoute string

const (
	routeChat      protocolRoute = "chat"
	routeMessages  protocolRoute = "messages"
	routeResponses protocolRoute = "responses"
)

// routeProtocol determines which Copilot API endpoint to use based on model
// name when discovery is unavailable or supported_endpoints is absent.
func routeProtocol(model string) protocolRoute {
	m := strings.ToLower(model)
	if strings.HasPrefix(m, "claude") {
		return routeMessages
	}
	if strings.Contains(m, "gpt-5") || strings.Contains(m, "codex") {
		return routeResponses
	}
	return routeChat
}

// ensureEndpoint discovers the Copilot API endpoint on first use, then caches
// it for the lifetime of the adapter.
func (l *LLM) ensureEndpoint(ctx context.Context) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.resolved {
		return l.endpoint, nil
	}

	l.endpoint = defaultEndpoint
	if ep, err := discoverEndpoint(ctx, l.client, l.config.Token); err == nil && ep != "" {
		l.endpoint = ep
	}
	l.resolved = true
	return l.endpoint, nil
}

// doChatCompletions sends a standard OpenAI Chat Completions request. No
// translation is needed — the request and response are already in OpenAI
// format.
func (l *LLM) doChatCompletions(ctx context.Context, endpoint string, request openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("copilot: marshal request: %w", err)
	}

	respBody, err := l.send(ctx, endpoint, "/chat/completions", body, false)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}

	var chatResp openai.ChatCompletionResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("copilot: parse chat response: %w", err)
	}

	usage := cogito.LLMUsage{
		PromptTokens:     chatResp.Usage.PromptTokens,
		CompletionTokens: chatResp.Usage.CompletionTokens,
		TotalTokens:      chatResp.Usage.TotalTokens,
	}
	return cogito.LLMReply{ChatCompletionResponse: chatResp}, usage, nil
}

// doResponses translates to the OpenAI Responses API format and sends to
// /responses.
func (l *LLM) doResponses(ctx context.Context, endpoint string, request openai.ChatCompletionRequest, summary *bool) (cogito.LLMReply, cogito.LLMUsage, error) {
	body, err := openairesponses.TranslateRequestWithSummary(request, openairesponses.SummaryOptions{Provider: "github-copilot", BaseURL: endpoint, Support: summary})
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("copilot: translate responses request: %w", err)
	}

	respBody, err := l.send(ctx, endpoint, "/responses", body, false)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}

	return openairesponses.TranslateResponse(respBody, request.Model)
}

// doMessages translates to the Anthropic Messages API format and sends to
// /v1/messages.
func (l *LLM) doMessages(ctx context.Context, endpoint string, request openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	body, err := anthropic.TranslateRequest(request)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("copilot: translate messages request: %w", err)
	}

	respBody, err := l.send(ctx, endpoint, "/v1/messages", body, true)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}

	return anthropic.TranslateResponse(respBody, request.Model)
}

// send posts a request body to the Copilot API with the required headers.
// isMessages controls whether the anthropic-version header is added.
func (l *LLM) send(ctx context.Context, endpoint, path string, body []byte, isMessages bool) ([]byte, error) {
	url := strings.TrimRight(endpoint, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("copilot: create request: %w", err)
	}

	l.setHeaders(req, isMessages)

	resp, err := l.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("copilot: request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("copilot: read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, parseAPIError(resp.StatusCode, respBody)
	}

	return respBody, nil
}

// setHeaders applies the Copilot-required headers to every request.
func (l *LLM) setHeaders(req *http.Request, isMessages bool) {
	req.Header.Set("Authorization", "Bearer "+l.config.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Editor-Version", editorVersion)
	req.Header.Set("X-GitHub-Api-Version", "2025-10-01")
	req.Header.Set("User-Agent", editorVersion)
	req.Header.Set("X-Initiator", "agent")
	req.Header.Set("X-Interaction-Type", "conversation-agent")
	req.Header.Set("Openai-Intent", "conversation-agent")
	if isMessages {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
}

// parseAPIError formats an HTTP error response body into a Go error.
func parseAPIError(status int, body []byte) error {
	var apiErr struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &apiErr); err == nil {
		if apiErr.Error.Message != "" {
			return fmt.Errorf("copilot: HTTP %d: %s", status, apiErr.Error.Message)
		}
		if apiErr.Message != "" {
			return fmt.Errorf("copilot: HTTP %d: %s", status, apiErr.Message)
		}
	}
	return fmt.Errorf("copilot: HTTP %d", status)
}
