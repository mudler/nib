package openairesponses

import (
	"github.com/mudler/cogito"
	openai "github.com/sashabaranov/go-openai"
)

// TranslateRequest converts an OpenAI Chat Completion request to an OpenAI
// Responses API JSON body. Exported for reuse by Azure OpenAI Responses
// and other Responses-API-compatible providers.
func TranslateRequest(req openai.ChatCompletionRequest) ([]byte, error) {
	var l LLM
	return l.translateRequest(req)
}

// TranslateResponse parses a Responses API JSON response into OpenAI types.
// Exported for reuse by Azure OpenAI Responses and other compatible adapters.
func TranslateResponse(body []byte, requestModel string) (cogito.LLMReply, cogito.LLMUsage, error) {
	var l LLM
	return l.translateResponse(body, requestModel)
}

// ParseAPIError formats an HTTP error response body into a Go error.
// Exported for reuse by Azure and other Responses-compatible adapters.
func ParseAPIError(status int, body []byte) error {
	return parseAPIError(status, body)
}

// ErrNoResponseSentinel is returned when the API completes without an
// assistant message. Exported for reuse.
var ErrNoResponseSentinel = ErrNoResponse

// SummaryOptions qualifies summary metadata by the actual provider route.
// Support is tri-state: nil is unknown; false overrides all defaults.
type SummaryOptions struct {
	Provider string
	BaseURL  string
	Support  *bool
}

// TranslateRequestWithSummary uses explicit or route-qualified summary metadata.
// Unlike the legacy translation entry point, unknown routes do not infer support.
func TranslateRequestWithSummary(req openai.ChatCompletionRequest, options SummaryOptions) ([]byte, error) {
	l := LLM{config: Config{summaryOptions: &options}}
	return l.translateRequest(req)
}
