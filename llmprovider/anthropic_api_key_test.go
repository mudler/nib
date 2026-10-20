package llmprovider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/types"
	"github.com/sashabaranov/go-openai"
)

type apiKeyTransport func(*http.Request) (*http.Response, error)

func (f apiKeyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAnthropicAPIKeyNativeRoute(t *testing.T) {
	for _, source := range []string{"stored", "config", "env", "missing", "incompatible-config", "incompatible-env", "incompatible-missing"} {
		t.Run(source, func(t *testing.T) {
			store := auth.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
			oauth := auth.Credential{ProviderID: "anthropic", Kind: auth.CredentialOAuth, AccessToken: "sentinel-oauth", RefreshToken: "sentinel-refresh", ExpiresAt: time.Now().Add(-time.Hour)}
			if err := store.Save(oauth); err != nil {
				t.Fatal(err)
			}
			before, _, _ := store.Get("anthropic")
			if strings.HasPrefix(source, "incompatible-") {
				incompatible := oauth
				incompatible.ProviderID = "anthropic-api-key"
				if err := store.Save(incompatible); err != nil {
					t.Fatal(err)
				}
			}
			keyBefore, _, _ := store.Get("anthropic-api-key")
			defer func() {
				if strings.HasPrefix(source, "incompatible-") {
					after, _, _ := store.Get("anthropic-api-key")
					if !reflect.DeepEqual(keyBefore, after) {
						t.Error("incompatible credential changed")
					}
				}
			}()
			cfg := types.ModelProviderConfig{Provider: "anthropic-api-key", Model: "claude-test"}
			t.Setenv("ANTHROPIC_API_KEY", "sentinel-env")
			want := "sentinel-env"
			if source == "stored" {
				if err := store.Save(auth.Credential{ProviderID: cfg.Provider, Kind: auth.CredentialAPIKey, APIKey: "sentinel-stored"}); err != nil {
					t.Fatal(err)
				}
				cfg.APIKey = "sentinel-config"
				want = "sentinel-stored"
			}
			if source == "config" || source == "incompatible-config" {
				cfg.APIKey = "sentinel-config"
				want = cfg.APIKey
			}
			if source == "missing" || source == "incompatible-missing" {
				t.Setenv("ANTHROPIC_API_KEY", "")
			}
			old := http.DefaultTransport
			defer func() { http.DefaultTransport = old }()
			calls := 0
			status := http.StatusOK
			http.DefaultTransport = apiKeyTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "api.anthropic.com" || r.Header.Get("x-api-key") != want || r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("Authorization") != "" || r.Header.Get("anthropic-beta") != "" {
					t.Fatal("wrong route or authentication headers")
				}
				body := `{"error":{"type":"authentication_error","message":"sentinel-denied"}}`
				if status == http.StatusOK {
					switch r.URL.Path {
					case "/v1/models":
						if r.Method != http.MethodGet {
							t.Fatal("discovery must use GET")
						}
						body = `{"data":[{"id":"claude-test"}],"has_more":false}`
					case "/v1/messages":
						var req struct {
							Stream   bool `json:"stream"`
							Messages []struct {
								Content json.RawMessage `json:"content"`
							} `json:"messages"`
						}
						if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
							t.Fatal(err)
						}
						if len(req.Messages) != 3 {
							t.Fatalf("wrong native message count: %d", len(req.Messages))
						}
						var text string
						if err := json.Unmarshal(req.Messages[0].Content, &text); err != nil || text != "lookup" {
							t.Fatalf("wrong native user text: %q, %v", text, err)
						}
						var blocks [2][]struct {
							Type      string `json:"type"`
							ToolUseID string `json:"tool_use_id"`
						}
						for i := range blocks {
							if err := json.Unmarshal(req.Messages[i+1].Content, &blocks[i]); err != nil {
								t.Fatal(err)
							}
						}
						if r.Method != http.MethodPost || req.Stream || len(blocks[0]) != 1 || len(blocks[1]) != 1 || blocks[0][0].Type != "tool_use" || blocks[1][0].Type != "tool_result" || blocks[1][0].ToolUseID != "call-one" {
							t.Fatalf("wrong native tool translation: %+v", req)
						}
						body = `{"id":"msg-one","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"done"},{"type":"tool_use","id":"call-two","name":"lookup","input":{"q":"next"}}],"stop_reason":"tool_use","usage":{"input_tokens":3,"output_tokens":4}}`
					default:
						t.Fatalf("unexpected path %s", r.URL.Path)
					}
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			llm, err := NewWithStore(cfg, store)
			if source == "missing" || source == "incompatible-missing" {
				if err == nil || !strings.Contains(err.Error(), "nib login anthropic-api-key") {
					t.Fatalf("missing-key error: %v", err)
				}
				if _, err := ListModels(context.Background(), cfg, store); err == nil {
					t.Fatal("discovery accepted missing key")
				}
				if calls != 0 {
					t.Fatal("missing credentials made HTTP calls")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := llm.(cogito.StreamingLLM); ok {
				t.Fatal("retain existing nonstreaming adapter")
			}
			ids, err := ListModels(context.Background(), cfg, store)
			if err != nil || !reflect.DeepEqual(ids, []string{"claude-test"}) {
				t.Fatalf("discovery: %v %v", ids, err)
			}
			req := openai.ChatCompletionRequest{Model: cfg.Model, Messages: []openai.ChatCompletionMessage{
				{Role: "user", Content: "lookup"},
				{Role: "assistant", ToolCalls: []openai.ToolCall{{ID: "call-one", Type: "function", Function: openai.FunctionCall{Name: "lookup", Arguments: `{"q":"one"}`}}}},
				{Role: "tool", ToolCallID: "call-one", Content: "result"},
			}}
			reply, usage, err := llm.CreateChatCompletion(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			msg := reply.ChatCompletionResponse.Choices[0].Message
			if msg.Content != "done" || len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID != "call-two" || usage.TotalTokens != 7 {
				t.Fatalf("wrong translated response: %+v %+v", msg, usage)
			}
			for _, code := range []int{401, 403} {
				status = code
				start := calls
				if _, _, err := llm.CreateChatCompletion(context.Background(), req); err == nil || !strings.Contains(err.Error(), "sentinel-denied") {
					t.Fatalf("completion auth failure: %v", err)
				}
				if _, err := ListModels(context.Background(), cfg, store); err == nil || !strings.Contains(err.Error(), "sentinel-denied") {
					t.Fatalf("discovery auth failure: %v", err)
				}
				if calls != start+2 {
					t.Fatal("auth failure retried or fell back to OAuth")
				}
			}
			after, _, _ := store.Get("anthropic")
			if !reflect.DeepEqual(before, after) {
				t.Fatal("OAuth credential changed")
			}
		})
	}
}
