package openairesponses

import (
	"encoding/json"
	"testing"

	"github.com/mudler/nib/llmprovider/catalog"
	openai "github.com/sashabaranov/go-openai"
)

func TestRouteSummaryMetadata(t *testing.T) {
	yes, no := true, false
	for _, tt := range []struct {
		name, provider, url, model string
		support                    *bool
		want                       string
	}{
		{"official", "openai", "https://api.openai.com", "gpt-5", nil, "auto"},
		{"copilot", "github-copilot", "https://api.githubcopilot.com", "gpt-5", nil, "auto"},
		{"false wins", "openai", "https://api.openai.com", "gpt-5", &no, ""},
		{"explicit future", "openai", "https://api.openai.com", "gpt-6-future", &yes, "auto"},
		{"unknown future", "openai", "https://api.openai.com", "gpt-6-future", nil, ""},
		{"custom", "openai", "https://proxy.example", "gpt-5", nil, ""},
		{"lookalike", "openai", "https://api.openai.com.evil", "gpt-5", nil, ""},
		{"path", "openai", "https://api.openai.com/custom", "gpt-5", nil, ""},
		{"provider mismatch", "unknown", "https://api.openai.com", "gpt-5", nil, ""},
		{"suffix", "openai", "https://api.openai.com", "vendor/gpt-5", nil, ""},
		{"case", "openai", "https://api.openai.com", "GPT-5", nil, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b, err := TranslateRequestWithSummary(openai.ChatCompletionRequest{Model: tt.model, ReasoningEffort: "xhigh"}, SummaryOptions{Provider: tt.provider, BaseURL: tt.url, Support: tt.support})
			if err != nil {
				t.Fatal(err)
			}
			var r responsesRequest
			if err := json.Unmarshal(b, &r); err != nil {
				t.Fatal(err)
			}
			if r.Reasoning == nil || r.Reasoning.Summary != tt.want || r.Reasoning.Effort != "xhigh" {
				t.Fatalf("reasoning=%+v", r.Reasoning)
			}
		})
	}
	for _, route := range []SummaryOptions{
		{Provider: "openai", BaseURL: "https://api.openai.com"},
		{Provider: "github-copilot", BaseURL: "https://api.githubcopilot.com"},
	} {
		for _, model := range []string{"gpt-6.1-sol", "gpt-6-sol"} {
			t.Run(route.Provider+"/"+model, func(t *testing.T) {
				m, ok := catalog.LookupResponsesRoute(route.Provider, model, route.BaseURL)
				if !ok || m.Compat.SupportsReasoningSummary == nil || !*m.Compat.SupportsReasoningSummary {
					t.Fatal("expected exact pinned catalog entry with summary support")
				}
				for _, effort := range []string{"", "xhigh", "none"} {
					body, err := TranslateRequestWithSummary(openai.ChatCompletionRequest{Model: model, ReasoningEffort: effort}, route)
					if err != nil {
						t.Fatal(err)
					}
					var r responsesRequest
					if err := json.Unmarshal(body, &r); err != nil {
						t.Fatal(err)
					}
					want := "auto"
					if effort == "none" {
						want = ""
					}
					if r.Reasoning == nil || r.Reasoning.Summary != want || r.Reasoning.Effort != effort {
						t.Fatalf("reasoning=%+v", r.Reasoning)
					}
				}
				denied := route
				denied.Support = &no
				body, err := TranslateRequestWithSummary(openai.ChatCompletionRequest{Model: model}, denied)
				if err != nil {
					t.Fatal(err)
				}
				var r responsesRequest
				if err := json.Unmarshal(body, &r); err != nil {
					t.Fatal(err)
				}
				if r.Reasoning != nil {
					t.Fatalf("explicit false lost to catalog: %+v", r.Reasoning)
				}
				if route.Provider == "openai" && !New(Config{Model: model}).summarySupported(model, false) {
					t.Fatal("direct adapter lost catalog support")
				}
			})
		}
	}
}

func TestCustomResponsesSummaryConservative(t *testing.T) {
	l := New(Config{BaseURL: "https://proxy.example", Model: "gpt-5", ReasoningEffort: "high"})
	b, err := l.translateRequest(openai.ChatCompletionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var r responsesRequest
	json.Unmarshal(b, &r)
	if r.Reasoning == nil || r.Reasoning.Summary != "" || r.Reasoning.Effort != "high" {
		t.Fatalf("reasoning=%+v", r.Reasoning)
	}
}
