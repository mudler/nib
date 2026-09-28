// Package codex adapts the OpenAI Codex Responses API (ChatGPT backend) to
// cogito.LLM.
//
// The Codex backend lives at https://chatgpt.com/backend-api/codex/responses
// and speaks the same Responses wire format as the standard OpenAI Responses
// API, but with Codex-specific constraints:
//   - No sampling parameters (temperature, top_p) or output caps
//   - store is always false; stream is always true (SSE)
//   - Codex identity headers (originator, version, chatgpt-account-id from JWT)
//   - include: ["reasoning.encrypted_content"]
//
// Response decoding reuses the openairesponses package. The SSE stream's
// terminal response.completed event carries the response object in the same
// JSON shape as a non-streaming Responses API response, except that its
// output array is empty: the items come in response.output_item.done events.
package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/llmprovider/openairesponses"
	openai "github.com/sashabaranov/go-openai"
)

const (
	codexBaseURL   = "https://chatgpt.com/backend-api"
	codexClientVer = "0.153.0"
	codexEndpoint  = "/codex/responses"

	headerRoutingHint     = "x-codex-routing-hint"
	headerResponsesLite   = "x-openai-internal-codex-responses-lite"
	headerContentEncoding = "Content-Encoding"
)

// Config holds the connection + auth parameters for the Codex adapter.
type Config struct {
	Model           string
	Token           string // OAuth access token (JWT)
	ReasoningEffort string // "", "none", "minimal", "low", "medium", "high", "xhigh", "max"
	ServiceTier     string // "", "auto", "default", "flex", "scale", "priority"
	ResponsesLite   bool
}

// LLM implements cogito.LLM against the Codex Responses API.
type LLM struct {
	config  Config
	client  *http.Client
	session *sessionState
}

var _ cogito.LLM = (*LLM)(nil)

// New returns a Codex adapter.
func New(config Config) *LLM {
	return &LLM{config: config, client: &http.Client{}, session: newSessionState()}
}

// ErrNoResponse is returned when the API completes without an assistant message.
var ErrNoResponse = errors.New("codex: completed without an assistant message")

// Ask delegates to CreateChatCompletion.
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

// CreateChatCompletion translates an OpenAI Chat Completion request to the
// Codex Responses API, calls it via SSE, and translates the response back.
func (l *LLM) CreateChatCompletion(ctx context.Context, request openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	meta := l.session.prepareTurn(request.Messages)

	body, err := l.translateRequest(request, meta)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}

	url := codexBaseURL + codexEndpoint

	// Compress request body with zstd (official endpoint only, best-effort).
	var compressed []byte
	if isOfficialCodexURL(url) {
		compressed = compressZstd(body)
	}

	useZstd := compressed != nil

	resp, err := l.sendRequest(ctx, url, body, compressed, meta, useZstd)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}

	// Retry without compression if server rejects zstd encoding.
	if useZstd && (resp.StatusCode == 400 || resp.StatusCode == 415) {
		resp.Body.Close()
		resp, err = l.sendRequest(ctx, url, body, nil, meta, false)
		if err != nil {
			return cogito.LLMReply{}, cogito.LLMUsage{}, err
		}
	}
	defer resp.Body.Close()

	l.session.captureTurnState(resp.Header)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("codex: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return cogito.LLMReply{}, cogito.LLMUsage{}, parseAPIError(resp.StatusCode, respBody)
	}

	responseJSON, err := extractCompletedResponse(respBody)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}

	return openairesponses.TranslateResponse(responseJSON, request.Model)
}

// setHeaders applies the Codex-specific request headers.
func (l *LLM) setHeaders(req *http.Request, meta requestMetadata) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+l.config.Token)
	req.Header.Set("originator", "nib")
	req.Header.Set("version", codexClientVer)
	req.Header.Set("OpenAI-Beta", "responses=experimental")

	if accountID := extractAccountID(l.config.Token); accountID != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	}
	if residency := extractResidency(l.config.Token); residency != "" {
		req.Header.Set("x-openai-internal-codex-residency", residency)
	}

	// Codex turn-state and client metadata headers.
	req.Header.Set(headerScopedSessionID, meta.SessionID)
	req.Header.Set(headerThreadID, meta.ThreadID)
	req.Header.Set(headerWindowID, meta.WindowID)
	if meta.TurnMetadataJSON != "" {
		req.Header.Set(headerTurnMetadata, meta.TurnMetadataJSON)
	}
	if meta.HasTurnState {
		req.Header.Set(headerTurnState, meta.TurnState)
	}

	// Routing hint: model and optional service tier.
	model := firstNonEmpty(l.config.Model, "")
	hint := "model=" + model
	if l.config.ServiceTier != "" && l.config.ServiceTier != "auto" {
		hint += ";tier=" + l.config.ServiceTier
	}
	req.Header.Set(headerRoutingHint, hint)

	// Responses Lite marker.
	if l.config.ResponsesLite {
		req.Header.Set(headerResponsesLite, "true")
	}
}

// sendRequest builds and sends the HTTP request, optionally with zstd
// compression.
func (l *LLM) sendRequest(ctx context.Context, url string, plainBody, compressedBody []byte, meta requestMetadata, useZstd bool) (*http.Response, error) {
	body := plainBody
	if useZstd && compressedBody != nil {
		body = compressedBody
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("codex: create request: %w", err)
	}
	l.setHeaders(req, meta)
	if useZstd {
		req.Header.Set(headerContentEncoding, "zstd")
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("codex: request: %w", err)
	}
	return resp, nil
}

// ---------------------------------------------------------------------------
// Request translation: openai.ChatCompletionRequest → Codex Responses body
// ---------------------------------------------------------------------------

type codexRequest struct {
	Model             string            `json:"model"`
	Input             []codexInput      `json:"input"`
	Instructions      string            `json:"instructions,omitempty"`
	Tools             []codexTool       `json:"tools,omitempty"`
	ToolChoice        any               `json:"tool_choice,omitempty"`
	Include           []string          `json:"include"`
	Store             bool              `json:"store"`
	Stream            bool              `json:"stream"`
	ClientMetadata    map[string]string `json:"client_metadata,omitempty"`
	Reasoning         *codexReasoning   `json:"reasoning,omitempty"`
	ServiceTier       string            `json:"service_tier,omitempty"`
	PromptCacheKey    string            `json:"prompt_cache_key,omitempty"`
	ParallelToolCalls *bool             `json:"parallel_tool_calls,omitempty"`
}

type codexReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
	Context string `json:"context,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

type codexInput struct {
	ID        string      `json:"id,omitempty"`
	Type      string      `json:"type,omitempty"`
	Role      string      `json:"role,omitempty"`
	Content   string      `json:"content,omitempty"`
	CallID    string      `json:"call_id,omitempty"`
	Name      string      `json:"name,omitempty"`
	Arguments string      `json:"arguments,omitempty"`
	Output    string      `json:"output,omitempty"`
	Tools     []codexTool `json:"tools,omitempty"`
}

type codexTool struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters"`
	Strict      bool   `json:"strict"`
}

func (l *LLM) translateRequest(req openai.ChatCompletionRequest, meta requestMetadata) ([]byte, error) {
	cr := codexRequest{
		Model:          firstNonEmpty(req.Model, l.config.Model),
		Store:          false,
		Stream:         true,
		Include:        []string{"reasoning.encrypted_content"},
		ClientMetadata: meta.clientMetadata(),
	}

	var input []codexInput
	var instructions []string

	for _, msg := range req.Messages {
		switch msg.Role {
		case openai.ChatMessageRoleSystem, openai.ChatMessageRoleDeveloper:
			if msg.Content != "" {
				instructions = append(instructions, msg.Content)
			}
		case openai.ChatMessageRoleUser:
			input = append(input, codexInput{
				Role:    "user",
				Content: msg.Content,
			})
		case openai.ChatMessageRoleAssistant:
			if msg.Content != "" {
				input = append(input, codexInput{
					Role:    "assistant",
					Content: msg.Content,
				})
			}
			for _, tc := range msg.ToolCalls {
				input = append(input, codexInput{
					Type:      "function_call",
					CallID:    tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}
		case openai.ChatMessageRoleTool:
			input = append(input, codexInput{
				Type:   "function_call_output",
				CallID: msg.ToolCallID,
				Output: msg.Content,
			})
		default:
			input = append(input, codexInput{
				Role:    "user",
				Content: msg.Content,
			})
		}
	}

	// Input repair pipeline: filter → sanitize call IDs → repair orphan pairs.
	input = filterInput(input)
	sanitizeInputCallIds(input)
	cr.Input = repairToolCallPairs(input)
	if len(instructions) > 0 {
		cr.Instructions = strings.Join(instructions, "\n\n")
	}
	if len(req.Tools) > 0 {
		cr.Tools = translateTools(req.Tools)
	}
	if req.ToolChoice != nil {
		cr.ToolChoice = translateToolChoice(req.ToolChoice)
	}

	// Prompt cache key — derived from session ID for stable caching across turns.
	cr.PromptCacheKey = meta.SessionID

	// Reasoning controls.
	if l.config.ReasoningEffort != "" || l.config.ResponsesLite {
		r := &codexReasoning{}
		if l.config.ReasoningEffort != "" {
			r.Effort = l.config.ReasoningEffort
		}
		if l.config.ResponsesLite {
			r.Context = "all_turns"
		}
		cr.Reasoning = r
	}

	// Service tier — "auto" is never sent (omitting is equivalent).
	if l.config.ServiceTier != "" && l.config.ServiceTier != "auto" {
		cr.ServiceTier = l.config.ServiceTier
	}

	// Responses Lite: restructure the body to pack tools into an
	// additional_tools input item, move instructions to a developer
	// message, force parallel_tool_calls=false, and delete top-level
	// instructions/tools.
	if l.config.ResponsesLite {
		applyResponsesLiteShape(&cr)
	}

	data, err := json.Marshal(cr)
	if err != nil {
		return nil, fmt.Errorf("codex: marshal request: %w", err)
	}
	return data, nil
}

// applyResponsesLiteShape restructures the Codex request body for the
// "Responses Lite" wire format. Tools are packed into an additional_tools
// input item, instructions move to a developer message, parallel_tool_calls
// is forced false, and the top-level instructions/tools fields are cleared.
func applyResponsesLiteShape(cr *codexRequest) {
	var prefix []codexInput

	if len(cr.Tools) > 0 {
		prefix = append(prefix, codexInput{
			Type:  "additional_tools",
			Role:  "developer",
			Tools: cr.Tools,
		})
	}
	if cr.Instructions != "" {
		prefix = append(prefix, codexInput{
			Type:    "message",
			Role:    "developer",
			Content: cr.Instructions,
		})
	}

	cr.Input = append(prefix, cr.Input...)
	falseVal := false
	cr.ParallelToolCalls = &falseVal
	cr.Instructions = ""
	cr.Tools = nil
}

func translateTools(tools []openai.Tool) []codexTool {
	out := make([]codexTool, 0, len(tools))
	for _, t := range tools {
		if t.Function == nil {
			continue
		}
		out = append(out, codexTool{
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

// ---------------------------------------------------------------------------
// SSE response parsing
// ---------------------------------------------------------------------------

// extractCompletedResponse reads an SSE stream, finds the terminal
// response.completed event, and returns its response field as JSON.
//
// The Codex backend sends response.completed with an empty output array and
// streams the items only in response.output_item.done events. When the
// completed output is empty, the collected items are put in its place, in
// output_index order.
func extractCompletedResponse(sseBody []byte) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(sseBody))
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	var dataLines []string
	var items []outputItem

	// handle processes one event. It returns the final response JSON, or an
	// error, when the event ends the stream.
	handle := func(data string) ([]byte, error) {
		if data == "[DONE]" {
			return nil, nil
		}
		var ev struct {
			Type        string          `json:"type"`
			Response    json.RawMessage `json:"response"`
			Item        json.RawMessage `json:"item"`
			OutputIndex int             `json:"output_index"`
			Error       *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil, nil
		}
		switch ev.Type {
		case "response.output_item.done":
			if len(ev.Item) > 0 {
				items = append(items, outputItem{index: ev.OutputIndex, raw: ev.Item})
			}
		case "response.completed":
			if len(ev.Response) > 0 {
				return fillOutput(ev.Response, items)
			}
		case "response.failed", "error":
			if ev.Error != nil && ev.Error.Message != "" {
				return nil, fmt.Errorf("codex: API error %s: %s", ev.Error.Code, ev.Error.Message)
			}
		}
		return nil, nil
	}

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// Event boundary — process accumulated data
			if len(dataLines) > 0 {
				data := strings.Join(dataLines, "\n")
				dataLines = dataLines[:0]
				if resp, err := handle(data); resp != nil || err != nil {
					return resp, err
				}
			}
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		} else if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
		}
	}

	// Process any remaining data after last event
	if len(dataLines) > 0 {
		if resp, err := handle(strings.Join(dataLines, "\n")); resp != nil || err != nil {
			return resp, err
		}
	}

	return nil, fmt.Errorf("codex: no response.completed event in SSE stream")
}

// outputItem is one item from a response.output_item.done event.
type outputItem struct {
	index int
	raw   json.RawMessage
}

// fillOutput returns the response with its output set to items when the
// response has no output of its own.
func fillOutput(response json.RawMessage, items []outputItem) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(response, &obj); err != nil {
		return nil, fmt.Errorf("codex: parse completed response: %w", err)
	}
	var output []json.RawMessage
	if raw, ok := obj["output"]; ok {
		_ = json.Unmarshal(raw, &output)
	}
	if len(output) > 0 || len(items) == 0 {
		return response, nil
	}

	sort.SliceStable(items, func(i, j int) bool { return items[i].index < items[j].index })
	output = make([]json.RawMessage, len(items))
	for i, it := range items {
		output[i] = it.raw
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("codex: marshal output: %w", err)
	}
	obj["output"] = raw
	return json.Marshal(obj)
}

func parseAPIError(status int, body []byte) error {
	var errResp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error.Message != "" {
		return fmt.Errorf("codex: %d %s: %s", status, errResp.Error.Code, errResp.Error.Message)
	}
	return fmt.Errorf("codex: HTTP %d: %s", status, strings.TrimSpace(string(body)))
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
