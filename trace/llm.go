package trace

import (
	"context"

	"github.com/mudler/cogito"
	"github.com/mudler/xlog"
	openai "github.com/sashabaranov/go-openai"
)

// recordingLLM wraps a cogito.LLM and appends a Record for every call.
// Recording never alters the wrapped LLM's return values or errors; a trace
// write failure is logged and swallowed.
type recordingLLM struct {
	inner   cogito.LLM
	rec     *Recorder
	model   string
	agentID string
}

// NewRecordingLLM wraps inner so each LLM call is appended to rec. model labels
// the records (cogito builds requests without a model; the underlying client
// fills it in), and agentID tags the source ("" for the main session).
func NewRecordingLLM(inner cogito.LLM, rec *Recorder, model, agentID string) cogito.LLM {
	return &recordingLLM{inner: inner, rec: rec, model: model, agentID: agentID}
}

func (l *recordingLLM) write(rec Record) {
	if err := l.rec.Record(rec); err != nil {
		xlog.Warn("trace: failed to record LLM call", "error", err)
	}
}

func (l *recordingLLM) CreateChatCompletion(ctx context.Context, request openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	reply, usage, err := l.inner.CreateChatCompletion(ctx, request)

	req := request
	if req.Model == "" {
		req.Model = l.model
	}
	rec := Record{
		Model:   l.model,
		AgentID: l.agentID,
		Method:  "chat_completion",
		Request: &req,
	}
	if err != nil {
		rec.Error = err.Error()
	} else {
		resp := reply.ChatCompletionResponse
		rec.Response = &resp
	}
	l.write(rec)

	return reply, usage, err
}

func (l *recordingLLM) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	result, err := l.inner.Ask(ctx, f)

	req := openai.ChatCompletionRequest{Model: l.model, Messages: f.GetMessages()}
	rec := Record{
		Model:   l.model,
		AgentID: l.agentID,
		Method:  "ask",
		Request: &req,
	}
	if err != nil {
		rec.Error = err.Error()
	} else {
		rec.Response = askResponse(l.model, result)
	}
	l.write(rec)

	return result, err
}

// askResponse synthesizes a response from the fragment Ask returns, whose last
// message is the assistant reply. cogito's Ask discards the raw API response, so
// this is a best-effort reconstruction (Ask is used for planning/summaries, not
// the agentic conversation).
func askResponse(model string, f cogito.Fragment) *openai.ChatCompletionResponse {
	resp := &openai.ChatCompletionResponse{Model: model}
	if n := len(f.Messages); n > 0 {
		resp.Choices = []openai.ChatCompletionChoice{{Index: 0, Message: f.Messages[n-1]}}
	}
	if f.Status != nil {
		resp.Usage = openai.Usage{
			PromptTokens:     f.Status.LastUsage.PromptTokens,
			CompletionTokens: f.Status.LastUsage.CompletionTokens,
			TotalTokens:      f.Status.LastUsage.TotalTokens,
		}
	}
	return resp
}
