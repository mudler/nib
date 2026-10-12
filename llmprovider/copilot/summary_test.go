package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	openai "github.com/sashabaranov/go-openai"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompletionSummaryMetadata(t *testing.T) {
	for _, tt := range []struct{ model, support, effort, summary string }{
		{"future", "true", "high", "auto"},
		{"gpt-6-future", "true", "", "auto"},
		{"gpt-5", "false", "high", ""},
		{"future", "true", "none", ""},
		{"future", "true", "xhigh", "auto"},
		{"gpt-5", "null", "high", ""},
	} {
		t.Run(tt.model+tt.support+tt.effort, func(t *testing.T) {
			lists, posts := 0, 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/models" {
					lists++
					fmt.Fprintf(w, `{"data":[{"id":%q,"supported_endpoints":["/responses"],"capabilities":{"supports":{"reasoning_summary":%s},"supported_reasoning_efforts":["high","xhigh","none"]}}]}`, tt.model, tt.support)
					return
				}
				posts++
				var body struct {
					Reasoning struct {
						Effort  string `json:"effort"`
						Summary string `json:"summary"`
					} `json:"reasoning"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Reasoning.Effort != tt.effort || body.Reasoning.Summary != tt.summary {
					t.Errorf("reasoning=%+v, want effort %s summary %s", body.Reasoning, tt.effort, tt.summary)
				}
				fmt.Fprint(w, `{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`)
			}))
			defer s.Close()
			fresh := func() *LLM {
				l := New(Config{Token: "test", Model: tt.model})
				l.endpoint = s.URL
				l.resolved = true
				return l
			}
			l := fresh()
			complete := func() {
				_, _, err := l.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{ReasoningEffort: tt.effort})
				if err != nil {
					t.Fatal(err)
				}
			}
			complete() // Completion discovers on its own, before any picker.
			if _, err := fresh().ListModels(context.Background()); err != nil {
				t.Fatal(err)
			}
			complete() // A separate picker cannot change this adapter's metadata.
			if _, err := l.ListModels(context.Background()); err != nil {
				t.Fatal(err)
			}
			complete()
			if lists != 3 || posts != 3 {
				t.Fatalf("lists/posts=%d/%d", lists, posts)
			}
		})
	}
}
