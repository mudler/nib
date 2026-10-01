package azureresponses

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestSharedRequestReasoningEffort(t *testing.T) {
	for _, effort := range []string{"high", "none"} {
		t.Run(effort, func(t *testing.T) {
			var got struct {
				Model     string
				Reasoning struct{ Effort, Summary string }
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				w.Write([]byte(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`))
			}))
			defer srv.Close()
			l := New(Config{BaseURL: srv.URL, Model: "gpt-5", DeploymentName: "my-deployment"})
			_, _, err := l.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: "gpt-5", ReasoningEffort: effort})
			if err != nil {
				t.Fatal(err)
			}
			summary := "auto"
			if effort == "none" {
				summary = ""
			}
			if got.Model != "my-deployment" || got.Reasoning.Effort != effort || got.Reasoning.Summary != summary {
				t.Fatalf("request = %+v, want deployment with effort %q summary %q", got, effort, summary)
			}
		})
	}
}
