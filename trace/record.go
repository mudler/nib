package trace

import (
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// Record is one captured LLM call, serialized as a single NDJSON line. The
// first five fields mirror voro/claudemaster's trace.Call so the transcript is
// directly consumable by its export pipeline; Request/Response are go-openai
// types and therefore already in the OpenAI-chat shape that pipeline targets.
type Record struct {
	Timestamp time.Time                      `json:"timestamp"`
	Provider  string                         `json:"provider"` // always "openai"
	Model     string                         `json:"model"`
	AgentID   string                         `json:"agent_id,omitempty"` // "" = main session
	Method    string                         `json:"method"`             // chat_completion | ask | stream
	Request   *openai.ChatCompletionRequest  `json:"request"`
	Response  *openai.ChatCompletionResponse `json:"response,omitempty"` // omitted on error
	Error     string                         `json:"error,omitempty"`
}
