package bedrock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/internal/fragmentreply"
	openai "github.com/sashabaranov/go-openai"
)

// Config holds the connection + auth parameters for the Bedrock adapter.
type Config struct {
	Model       string
	BaseURL     string // default derived from region
	BearerToken string // optional Bedrock API key (Authorization: Bearer)
	Region      string // AWS region; defaults to env/​us-east-1
	Profile     string // AWS profile name; defaults to env/​"default"
}

// LLM implements cogito.LLM against the Bedrock Converse Stream API.
type LLM struct {
	config Config
	client *http.Client
}

var _ cogito.LLM = (*LLM)(nil)

// New returns a Bedrock adapter. If BaseURL is empty, it is derived from the
// resolved region as https://bedrock-runtime.{region}.amazonaws.com.
func New(config Config) *LLM {
	region := config.Region
	if region == "" {
		region = resolveRegion()
	}
	if config.BaseURL == "" {
		config.BaseURL = "https://bedrock-runtime." + region + ".amazonaws.com"
	}
	config.Region = region
	return &LLM{config: config, client: &http.Client{}}
}

// ErrNoResponse is returned when the API returns a message with no content.
var ErrNoResponse = errors.New("bedrock: completed without an assistant message")

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

// CreateChatCompletion translates an OpenAI Chat Completion request to the
// Bedrock Converse Stream API, calls it, and translates the response back.
func (l *LLM) CreateChatCompletion(ctx context.Context, request openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	body, err := l.translateRequest(request)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, err
	}

	u, err := url.Parse(l.config.BaseURL)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("bedrock: parse base URL: %w", err)
	}
	host := u.Host
	urlPath := strings.TrimRight(u.Path, "/") + "/model/" + url.PathEscape(l.config.Model) + "/converse-stream"
	fullURL := u.Scheme + "://" + host + urlPath
	if u.RawQuery != "" {
		fullURL += "?" + u.RawQuery
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(body))
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("bedrock: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.amazon.eventstream")

	if l.config.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+l.config.BearerToken)
	} else {
		creds, err := resolveCredentials(l.config.Profile)
		if err != nil {
			return cogito.LLMReply{}, cogito.LLMUsage{}, err
		}
		signed := signRequest(signParams{
			Method:  "POST",
			Host:    host,
			Path:    urlPath,
			Query:   u.RawQuery,
			Headers: map[string]string{"content-type": "application/json", "accept": "application/vnd.amazon.eventstream"},
			Body:    body,
			Region:  l.config.Region,
			Service: "bedrock",
			Creds:   creds,
		})
		req.Header.Set("Host", signed.Host)
		req.Header.Set("X-Amz-Date", signed.AmzDate)
		req.Header.Set("X-Amz-Content-Sha256", signed.AmzContentSHA256)
		req.Header.Set("Authorization", signed.Authorization)
		if signed.AmzSecurityToken != "" {
			req.Header.Set("X-Amz-Security-Token", signed.AmzSecurityToken)
		}
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("bedrock: request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("bedrock: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return cogito.LLMReply{}, cogito.LLMUsage{}, parseAPIError(resp.StatusCode, respBody)
	}

	return l.translateResponse(respBody, request.Model)
}

// ---------------------------------------------------------------------------
// Request translation: openai.ChatCompletionRequest → Bedrock Converse body
// ---------------------------------------------------------------------------

type converseRequest struct {
	Messages        []wireMessage       `json:"messages"`
	System          []wireContentBlock  `json:"system,omitempty"`
	InferenceConfig wireInferenceConfig `json:"inferenceConfig"`
	ToolConfig      *wireToolConfig     `json:"toolConfig,omitempty"`
}

type wireMessage struct {
	Role    string             `json:"role"` // "user" | "assistant"
	Content []wireContentBlock `json:"content"`
}

// wireContentBlock is a discriminated union: exactly one of the pointer fields
// is non-nil. JSON omits the nil ones, producing {"text":"..."} or
// {"toolUse":{...}} etc. as Bedrock expects.
type wireContentBlock struct {
	Text             *string              `json:"text,omitempty"`
	Image            *wireImageBlock      `json:"image,omitempty"`
	ToolUse          *wireToolUseBlock    `json:"toolUse,omitempty"`
	ToolResult       *wireToolResultBlock `json:"toolResult,omitempty"`
	ReasoningContent *wireReasoningBlock  `json:"reasoningContent,omitempty"`
}

type wireImageBlock struct {
	Format string          `json:"format"`
	Source wireImageSource `json:"source"`
}

type wireImageSource struct {
	Bytes string `json:"bytes"` // base64-encoded
}

type wireToolUseBlock struct {
	ToolUseID string          `json:"toolUseId"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input,omitempty"`
}

type wireToolResultBlock struct {
	ToolUseID string             `json:"toolUseId"`
	Content   []wireContentBlock `json:"content"`
	Status    string             `json:"status"` // "success" | "error"
}

type wireReasoningBlock struct {
	ReasoningText wireReasoningText `json:"reasoningText"`
}

type wireReasoningText struct {
	Text string `json:"text"`
}

type wireInferenceConfig struct {
	MaxTokens   int     `json:"maxTokens,omitempty"`
	Temperature float32 `json:"temperature,omitempty"`
	TopP        float32 `json:"topP,omitempty"`
}

type wireToolConfig struct {
	Tools      []wireToolSpec  `json:"tools,omitempty"`
	ToolChoice *wireToolChoice `json:"toolChoice,omitempty"`
}

type wireToolSpec struct {
	ToolSpec wireToolSpecBody `json:"toolSpec"`
}

type wireToolSpecBody struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema wireInputSchema `json:"inputSchema"`
}

type wireInputSchema struct {
	JSON json.RawMessage `json:"json"`
}

type wireToolChoice struct {
	Auto *struct{}        `json:"auto,omitempty"`
	Any  *struct{}        `json:"any,omitempty"`
	Tool *wireToolChoiceT `json:"tool,omitempty"`
}

type wireToolChoiceT struct {
	Name string `json:"name"`
}

func strPtr(s string) *string { return &s }

func (l *LLM) translateRequest(req openai.ChatCompletionRequest) ([]byte, error) {
	cr := converseRequest{
		Messages: make([]wireMessage, 0, len(req.Messages)),
	}

	// Build tool-call-ID → function-name map for tool-result messages.
	toolCallNames := make(map[string]string)
	for _, msg := range req.Messages {
		if msg.Role == openai.ChatMessageRoleAssistant {
			for _, tc := range msg.ToolCalls {
				toolCallNames[tc.ID] = tc.Function.Name
			}
		}
	}

	for i := 0; i < len(req.Messages); i++ {
		msg := req.Messages[i]
		switch msg.Role {
		case openai.ChatMessageRoleSystem, openai.ChatMessageRoleDeveloper:
			if msg.Content != "" {
				cr.System = append(cr.System, wireContentBlock{Text: strPtr(msg.Content)})
			}

		case openai.ChatMessageRoleUser:
			if len(msg.MultiContent) == 0 {
				if strings.TrimSpace(msg.Content) != "" {
					cr.Messages = append(cr.Messages, wireMessage{
						Role:    "user",
						Content: []wireContentBlock{{Text: strPtr(msg.Content)}},
					})
				}
			} else {
				blocks := translateUserMultiContent(msg.MultiContent)
				if len(blocks) > 0 {
					cr.Messages = append(cr.Messages, wireMessage{Role: "user", Content: blocks})
				}
			}

		case openai.ChatMessageRoleAssistant:
			blocks := translateAssistantContent(msg)
			if len(blocks) > 0 {
				cr.Messages = append(cr.Messages, wireMessage{Role: "assistant", Content: blocks})
			}

		case openai.ChatMessageRoleTool:
			// Collect consecutive tool-result messages into one user message.
			// Bedrock requires all tool results in a single user message.
			var resultBlocks []wireContentBlock
			for j := i; j < len(req.Messages); j++ {
				if req.Messages[j].Role != openai.ChatMessageRoleTool {
					break
				}
				status := "success"
				if strings.Contains(req.Messages[j].Content, `"error"`) {
					// Heuristic: the openai format doesn't carry an error flag on
					// tool messages, but callers may set content to indicate error.
					status = "error"
				}
				resultBlocks = append(resultBlocks, wireContentBlock{
					ToolResult: &wireToolResultBlock{
						ToolUseID: req.Messages[j].ToolCallID,
						Content:   []wireContentBlock{{Text: strPtr(req.Messages[j].Content)}},
						Status:    status,
					},
				})
				i = j
			}
			if len(resultBlocks) > 0 {
				cr.Messages = append(cr.Messages, wireMessage{Role: "user", Content: resultBlocks})
			}

		default:
			if strings.TrimSpace(msg.Content) != "" {
				cr.Messages = append(cr.Messages, wireMessage{
					Role:    "user",
					Content: []wireContentBlock{{Text: strPtr(msg.Content)}},
				})
			}
		}
	}

	// Inference config.
	if req.MaxTokens > 0 {
		cr.InferenceConfig.MaxTokens = req.MaxTokens
	}
	if req.MaxCompletionTokens > 0 {
		cr.InferenceConfig.MaxTokens = req.MaxCompletionTokens
	}
	if req.Temperature > 0 {
		cr.InferenceConfig.Temperature = req.Temperature
	}
	if req.TopP > 0 {
		cr.InferenceConfig.TopP = req.TopP
	}

	// Tools.
	if len(req.Tools) > 0 {
		cr.ToolConfig = buildToolConfig(req.Tools, req.ToolChoice)
	} else {
		// Bedrock requires a toolConfig when message history contains toolUse/toolResult.
		for _, m := range cr.Messages {
			for _, b := range m.Content {
				if b.ToolUse != nil || b.ToolResult != nil {
					cr.ToolConfig = &wireToolConfig{ToolChoice: &wireToolChoice{Auto: &struct{}{}}}
					break
				}
			}
			if cr.ToolConfig != nil {
				break
			}
		}
	}

	data, err := json.Marshal(cr)
	if err != nil {
		return nil, fmt.Errorf("bedrock: marshal request: %w", err)
	}
	return data, nil
}

func translateUserMultiContent(parts []openai.ChatMessagePart) []wireContentBlock {
	var blocks []wireContentBlock
	for _, part := range parts {
		switch part.Type {
		case openai.ChatMessagePartTypeText:
			if strings.TrimSpace(part.Text) != "" {
				blocks = append(blocks, wireContentBlock{Text: strPtr(part.Text)})
			}
		case openai.ChatMessagePartTypeImageURL:
			if img := extractImage(part.ImageURL.URL); img != nil {
				blocks = append(blocks, wireContentBlock{Image: img})
			}
		}
	}
	return blocks
}

func translateAssistantContent(msg openai.ChatCompletionMessage) []wireContentBlock {
	var blocks []wireContentBlock
	if strings.TrimSpace(msg.Content) != "" {
		blocks = append(blocks, wireContentBlock{Text: strPtr(msg.Content)})
	}
	if msg.ReasoningContent != "" {
		blocks = append(blocks, wireContentBlock{
			ReasoningContent: &wireReasoningBlock{
				ReasoningText: wireReasoningText{Text: msg.ReasoningContent},
			},
		})
	}
	for _, tc := range msg.ToolCalls {
		var input json.RawMessage
		if tc.Function.Arguments != "" {
			input = json.RawMessage(tc.Function.Arguments)
		} else {
			input = json.RawMessage("{}")
		}
		blocks = append(blocks, wireContentBlock{
			ToolUse: &wireToolUseBlock{
				ToolUseID: tc.ID,
				Name:      tc.Function.Name,
				Input:     input,
			},
		})
	}
	return blocks
}

func buildToolConfig(tools []openai.Tool, choice any) *wireToolConfig {
	specs := make([]wireToolSpec, 0, len(tools))
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
		if params == nil {
			params = json.RawMessage(`{"type":"object"}`)
		}
		specs = append(specs, wireToolSpec{
			ToolSpec: wireToolSpecBody{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				InputSchema: wireInputSchema{JSON: params},
			},
		})
	}
	tc := &wireToolConfig{Tools: specs}
	tc.ToolChoice = translateToolChoice(choice)
	return tc
}

func translateToolChoice(choice any) *wireToolChoice {
	switch v := choice.(type) {
	case string:
		switch v {
		case "auto":
			return &wireToolChoice{Auto: &struct{}{}}
		case "required":
			return &wireToolChoice{Any: &struct{}{}}
		case "none":
			return nil
		}
	case map[string]any:
		if v["type"] == "function" {
			if fn, ok := v["function"].(map[string]any); ok {
				if name, ok := fn["name"].(string); ok {
					return &wireToolChoice{Tool: &wireToolChoiceT{Name: name}}
				}
			}
		}
	}
	return nil
}

func extractImage(rawURL string) *wireImageBlock {
	if !strings.HasPrefix(rawURL, "data:") {
		return nil
	}
	comma := strings.Index(rawURL, ",")
	if comma < 0 {
		return nil
	}
	mime := rawURL[5:comma]
	b64 := rawURL[comma+1:]
	format := ""
	switch mime {
	case "image/jpeg", "image/jpg":
		format = "jpeg"
	case "image/png":
		format = "png"
	case "image/gif":
		format = "gif"
	case "image/webp":
		format = "webp"
	default:
		return nil
	}
	return &wireImageBlock{Format: format, Source: wireImageSource{Bytes: b64}}
}

// ---------------------------------------------------------------------------
// Response translation: eventstream → openai.ChatCompletionResponse
// ---------------------------------------------------------------------------

type messageStartEvent struct {
	Role string `json:"role"`
}

type contentBlockStartEvent struct {
	ContentBlockIndex int                `json:"contentBlockIndex"`
	Start             *contentBlockStart `json:"start,omitempty"`
}

type contentBlockStart struct {
	ToolUse *contentBlockToolUseStart `json:"toolUse,omitempty"`
}

type contentBlockToolUseStart struct {
	ToolUseID string `json:"toolUseId"`
	Name      string `json:"name"`
}

type contentBlockDeltaEvent struct {
	ContentBlockIndex int                `json:"contentBlockIndex"`
	Delta             *contentBlockDelta `json:"delta,omitempty"`
}

type contentBlockDelta struct {
	Text             *string                   `json:"text,omitempty"`
	ToolUse          *contentBlockToolUseDelta `json:"toolUse,omitempty"`
	ReasoningContent *contentBlockReasoning    `json:"reasoningContent,omitempty"`
}

type contentBlockToolUseDelta struct {
	Input string `json:"input"`
}

type contentBlockReasoning struct {
	Text      string `json:"text,omitempty"`
	Signature string `json:"signature,omitempty"`
}

type messageStopEvent struct {
	StopReason string `json:"stopReason,omitempty"`
}

type metadataEvent struct {
	Usage *wireUsage `json:"usage,omitempty"`
}

type wireUsage struct {
	InputTokens  int `json:"inputTokens,omitempty"`
	OutputTokens int `json:"outputTokens,omitempty"`
	TotalTokens  int `json:"totalTokens,omitempty"`
}

// streamingBlock tracks the state of one content block across deltas.
type streamingBlock struct {
	blockIndex int
	kind       string // "text", "toolUse", "reasoning"
	text       strings.Builder
	toolInput  strings.Builder
	toolID     string
	toolName   string
	thinking   strings.Builder
	signature  strings.Builder
}

func (l *LLM) translateResponse(body []byte, requestModel string) (cogito.LLMReply, cogito.LLMUsage, error) {
	messages, err := decodeEventStream(body)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("bedrock: decode eventstream: %w", err)
	}

	var contentText strings.Builder
	var thinkingText strings.Builder
	var toolCalls []openai.ToolCall
	var stopReason openai.FinishReason
	var usage cogito.LLMUsage

	blocks := make(map[int]*streamingBlock)
	var blockOrder []*streamingBlock

	for _, msg := range messages {
		messageType := msg.Headers[":message-type"]
		eventType := msg.Headers[":event-type"]

		if messageType == "exception" {
			excType := msg.Headers[":exception-type"]
			if excType == "" {
				excType = "Exception"
			}
			errMsg := extractErrorMessage(msg.Payload)
			return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("bedrock: %s: %s", excType, errMsg)
		}
		if messageType == "error" {
			code := msg.Headers[":error-code"]
			if code == "" {
				code = "UnknownError"
			}
			errMsg := msg.Headers[":error-message"]
			if errMsg == "" {
				errMsg = string(msg.Payload)
			}
			return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("bedrock: %s: %s", code, errMsg)
		}
		if messageType != "event" {
			continue
		}

		switch eventType {
		case "messageStart":
			// No action needed — role is always "assistant".

		case "contentBlockStart":
			var ev contentBlockStartEvent
			if err := json.Unmarshal(msg.Payload, &ev); err != nil {
				continue
			}
			blk := &streamingBlock{blockIndex: ev.ContentBlockIndex}
			if ev.Start != nil && ev.Start.ToolUse != nil {
				blk.kind = "toolUse"
				blk.toolID = ev.Start.ToolUse.ToolUseID
				blk.toolName = ev.Start.ToolUse.Name
			} else {
				blk.kind = "text"
			}
			blocks[ev.ContentBlockIndex] = blk
			blockOrder = append(blockOrder, blk)

		case "contentBlockDelta":
			var ev contentBlockDeltaEvent
			if err := json.Unmarshal(msg.Payload, &ev); err != nil {
				continue
			}
			blk, ok := blocks[ev.ContentBlockIndex]
			if !ok {
				blk = &streamingBlock{blockIndex: ev.ContentBlockIndex, kind: "text"}
				blocks[ev.ContentBlockIndex] = blk
				blockOrder = append(blockOrder, blk)
			}
			if ev.Delta == nil {
				continue
			}
			if ev.Delta.Text != nil {
				blk.text.WriteString(*ev.Delta.Text)
			}
			if ev.Delta.ToolUse != nil {
				blk.kind = "toolUse"
				blk.toolInput.WriteString(ev.Delta.ToolUse.Input)
			}
			if ev.Delta.ReasoningContent != nil {
				if blk.kind == "text" && blk.text.Len() == 0 {
					blk.kind = "reasoning"
				}
				if ev.Delta.ReasoningContent.Text != "" {
					blk.thinking.WriteString(ev.Delta.ReasoningContent.Text)
				}
				if ev.Delta.ReasoningContent.Signature != "" {
					blk.signature.WriteString(ev.Delta.ReasoningContent.Signature)
				}
			}

		case "contentBlockStop":
			// Block content is finalized; collection happens at messageStop.

		case "messageStop":
			var ev messageStopEvent
			_ = json.Unmarshal(msg.Payload, &ev)
			stopReason = mapStopReason(ev.StopReason)

		case "metadata":
			var ev metadataEvent
			if err := json.Unmarshal(msg.Payload, &ev); err == nil && ev.Usage != nil {
				usage.PromptTokens = ev.Usage.InputTokens
				usage.CompletionTokens = ev.Usage.OutputTokens
				usage.TotalTokens = ev.Usage.TotalTokens
				if usage.TotalTokens == 0 {
					usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
				}
			}
		}
	}

	// Collect content from blocks in arrival order.
	var toolCallIdx int
	for _, blk := range blockOrder {
		switch blk.kind {
		case "text":
			contentText.WriteString(blk.text.String())
		case "reasoning":
			thinkingText.WriteString(blk.thinking.String())
		case "toolUse":
			args := blk.toolInput.String()
			if args == "" {
				args = "{}"
			}
			toolCalls = append(toolCalls, openai.ToolCall{
				Index: &toolCallIdx,
				ID:    blk.toolID,
				Type:  openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      blk.toolName,
					Arguments: args,
				},
			})
			toolCallIdx++
		}
	}

	content := contentText.String()
	thinking := thinkingText.String()

	if content == "" && len(toolCalls) == 0 && thinking == "" {
		return cogito.LLMReply{}, cogito.LLMUsage{}, ErrNoResponse
	}

	if stopReason == "" {
		if len(toolCalls) > 0 {
			stopReason = openai.FinishReasonToolCalls
		} else {
			stopReason = openai.FinishReasonStop
		}
	}

	response := openai.ChatCompletionResponse{
		ID:    "bedrock-converse",
		Model: firstNonEmpty(requestModel, l.config.Model),
		Choices: []openai.ChatCompletionChoice{{
			Index: 0,
			Message: openai.ChatCompletionMessage{
				Role:             openai.ChatMessageRoleAssistant,
				Content:          content,
				ToolCalls:        toolCalls,
				ReasoningContent: thinking,
			},
			FinishReason: stopReason,
		}},
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
	case "max_tokens", "model_context_window_exceeded":
		return openai.FinishReasonLength
	case "tool_use":
		return openai.FinishReasonToolCalls
	default:
		return openai.FinishReasonStop
	}
}

func extractErrorMessage(payload []byte) string {
	var m map[string]string
	if err := json.Unmarshal(payload, &m); err == nil {
		if msg, ok := m["message"]; ok && msg != "" {
			return msg
		}
	}
	return string(payload)
}

func parseAPIError(status int, body []byte) error {
	return fmt.Errorf("bedrock: HTTP %d: %s", status, strings.TrimSpace(string(body)))
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
