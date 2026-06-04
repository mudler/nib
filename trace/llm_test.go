package trace

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/cogito"
	openai "github.com/sashabaranov/go-openai"
)

// fakeLLM is a minimal cogito.LLM for tests. It does NOT implement StreamingLLM.
type fakeLLM struct {
	reply   cogito.LLMReply
	askFrag cogito.Fragment
	ccErr   error
	askErr  error
}

func (f *fakeLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	if f.ccErr != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, f.ccErr
	}
	return f.reply, cogito.LLMUsage{}, nil
}

func (f *fakeLLM) Ask(ctx context.Context, frag cogito.Fragment) (cogito.Fragment, error) {
	if f.askErr != nil {
		return cogito.Fragment{}, f.askErr
	}
	return f.askFrag, nil
}

// readRecords parses every line of the transcript in dir.
func readRecords(t *testing.T, dir string) []Record {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, "trace.ndjson"))
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	defer f.Close()
	var out []Record
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("bad json line: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func TestRecordingLLMRecordsCompletion(t *testing.T) {
	dir := t.TempDir()
	rec, _ := NewRecorder(dir)
	defer rec.Close()

	inner := &fakeLLM{reply: cogito.LLMReply{ChatCompletionResponse: openai.ChatCompletionResponse{
		Model:   "srv-model",
		Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Role: "assistant", Content: "hi"}}},
		Usage:   openai.Usage{PromptTokens: 3, CompletionTokens: 1, TotalTokens: 4},
	}}}

	llm := NewRecordingLLM(inner, rec, "cfg-model", "")
	reply, _, err := llm.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "yo"}},
	})
	if err != nil {
		t.Fatalf("CreateChatCompletion: %v", err)
	}
	if reply.ChatCompletionResponse.Choices[0].Message.Content != "hi" {
		t.Fatal("return value not forwarded verbatim")
	}

	recs := readRecords(t, dir)
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	r := recs[0]
	if r.Method != "chat_completion" {
		t.Fatalf("method = %q", r.Method)
	}
	if r.Model != "cfg-model" {
		t.Fatalf("model = %q, want cfg-model", r.Model)
	}
	if r.Error != "" {
		t.Fatalf("unexpected error field: %q", r.Error)
	}
	if r.Request == nil || r.Request.Model != "cfg-model" {
		t.Fatalf("request model not backfilled: %+v", r.Request)
	}
	if r.Response == nil || r.Response.Choices[0].Message.Content != "hi" {
		t.Fatalf("response not recorded: %+v", r.Response)
	}
}

func TestRecordingLLMRecordsCompletionError(t *testing.T) {
	dir := t.TempDir()
	rec, _ := NewRecorder(dir)
	defer rec.Close()

	wantErr := errors.New("boom")
	llm := NewRecordingLLM(&fakeLLM{ccErr: wantErr}, rec, "cfg-model", "")
	_, _, err := llm.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error not forwarded: %v", err)
	}

	recs := readRecords(t, dir)
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if recs[0].Error != "boom" {
		t.Fatalf("error field = %q, want boom", recs[0].Error)
	}
	if recs[0].Response != nil {
		t.Fatal("response must be omitted on error")
	}
}

func TestRecordingLLMRecordsAsk(t *testing.T) {
	dir := t.TempDir()
	rec, _ := NewRecorder(dir)
	defer rec.Close()

	frag := cogito.Fragment{
		Messages: []openai.ChatCompletionMessage{
			{Role: "user", Content: "q"},
			{Role: "assistant", Content: "a"},
		},
		Status: &cogito.Status{LastUsage: cogito.LLMUsage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7}},
	}
	llm := NewRecordingLLM(&fakeLLM{askFrag: frag}, rec, "cfg-model", "")

	got, err := llm.Ask(context.Background(), cogito.Fragment{Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "q"}}})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatal("Ask return value not forwarded")
	}

	recs := readRecords(t, dir)
	if len(recs) != 1 || recs[0].Method != "ask" {
		t.Fatalf("ask record missing: %+v", recs)
	}
	if recs[0].Response == nil || recs[0].Response.Choices[0].Message.Content != "a" {
		t.Fatalf("ask response not reconstructed: %+v", recs[0].Response)
	}
	if recs[0].Response.Usage.TotalTokens != 7 {
		t.Fatalf("ask usage not reconstructed: %+v", recs[0].Response.Usage)
	}
}

func TestNewRecordingLLMPlainIsNotStreaming(t *testing.T) {
	llm := NewRecordingLLM(&fakeLLM{}, nil, "m", "")
	if _, ok := llm.(cogito.StreamingLLM); ok {
		t.Fatal("wrapping a non-streaming LLM must not yield a StreamingLLM")
	}
}
