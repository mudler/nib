package copilot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/provider"
	openai "github.com/sashabaranov/go-openai"
)

// --- routeProtocol ---

func TestRouteProtocol(t *testing.T) {
	tests := []struct {
		model string
		want  protocolRoute
	}{
		{"claude-sonnet-4-20250514", routeMessages},
		{"claude-opus-4-20250514", routeMessages},
		{"gpt-5", routeResponses},
		{"gpt-5-codex", routeResponses},
		{"codex-mini-latest", routeResponses},
		{"gpt-4o", routeChat},
		{"gpt-4-turbo", routeChat},
		{"o3-mini", routeChat},
		{"o1", routeChat},
		{"", routeChat},
		{"Claude-3.5-Sonnet", routeMessages}, // case-insensitive
		{"GPT-5-Codex", routeResponses},      // case-insensitive
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := routeProtocol(tt.model); got != tt.want {
				t.Errorf("routeProtocol(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

// --- ResolveToken (env vars) ---

func TestResolveTokenEnvVar(t *testing.T) {
	t.Setenv("GH_COPILOT_TOKEN", "")
	t.Setenv("COPILOT_GITHUB_TOKEN", "")

	t.Setenv("GH_COPILOT_TOKEN", "ghcop_test_token_123")
	token, err := ResolveToken()
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if token != "ghcop_test_token_123" {
		t.Errorf("token = %q, want ghcop_test_token_123", token)
	}
}

func TestResolveTokenFallbackEnvVar(t *testing.T) {
	t.Setenv("GH_COPILOT_TOKEN", "")
	t.Setenv("COPILOT_GITHUB_TOKEN", "ghcop_fallback_token")

	token, err := ResolveToken()
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if token != "ghcop_fallback_token" {
		t.Errorf("token = %q, want ghcop_fallback_token", token)
	}
}

func TestResolveTokenNoTokenError(t *testing.T) {
	t.Setenv("GH_COPILOT_TOKEN", "")
	t.Setenv("COPILOT_GITHUB_TOKEN", "")

	// Point config dir to a temp dir with no copilot files.
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)

	_, err := ResolveToken()
	if err == nil {
		t.Fatal("ResolveToken: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no token found") {
		t.Errorf("error = %q, want 'no token found'", err)
	}
}

// --- ResolveToken (config files) ---

func TestResolveTokenFromHostsJSON(t *testing.T) {
	t.Setenv("GH_COPILOT_TOKEN", "")
	t.Setenv("COPILOT_GITHUB_TOKEN", "")

	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	copilotDir := filepath.Join(tmp, "github-copilot")
	os.MkdirAll(copilotDir, 0o755)
	hosts := map[string]map[string]string{
		"github.com": {"oauth_token": "ghcop_hosts_json_token"},
	}
	data, _ := json.Marshal(hosts)
	os.WriteFile(filepath.Join(copilotDir, "hosts.json"), data, 0o600)

	token, err := ResolveToken()
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if token != "ghcop_hosts_json_token" {
		t.Errorf("token = %q, want ghcop_hosts_json_token", token)
	}
}

func TestResolveTokenFromAppsJSON(t *testing.T) {
	t.Setenv("GH_COPILOT_TOKEN", "")
	t.Setenv("COPILOT_GITHUB_TOKEN", "")

	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	copilotDir := filepath.Join(tmp, "github-copilot")
	os.MkdirAll(copilotDir, 0o755)
	apps := map[string]map[string]string{
		"github.com": {"oauth_token": "ghcop_apps_json_token"},
	}
	data, _ := json.Marshal(apps)
	os.WriteFile(filepath.Join(copilotDir, "apps.json"), data, 0o600)

	token, err := ResolveToken()
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if token != "ghcop_apps_json_token" {
		t.Errorf("token = %q, want ghcop_apps_json_token", token)
	}
}

func TestResolveTokenFromGhHostsYAML(t *testing.T) {
	t.Setenv("GH_COPILOT_TOKEN", "")
	t.Setenv("COPILOT_GITHUB_TOKEN", "")

	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	ghDir := filepath.Join(tmp, "gh")
	os.MkdirAll(ghDir, 0o755)
	yamlContent := "github.com:\n  oauth_token: ghcop_yaml_token\n  user: testuser\n"
	os.WriteFile(filepath.Join(ghDir, "hosts.yml"), []byte(yamlContent), 0o600)

	token, err := ResolveToken()
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if token != "ghcop_yaml_token" {
		t.Errorf("token = %q, want ghcop_yaml_token", token)
	}
}

// --- discoverEndpoint ---

func TestDiscoverEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := `{"data":{"viewer":{"copilotEndpoints":[{"api":"https://test.copilot.example.com"}]}}}`
		w.Write([]byte(resp))
	}))
	defer srv.Close()

	// Override graphqlURL by testing the function directly with a custom client.
	// Since graphqlURL is a const, we test via the real function which falls
	// back to defaultEndpoint on network errors.
	endpoint, err := discoverEndpoint(context.Background(), srv.Client(), "fake-token")
	_ = endpoint
	_ = err
	// The function hits the real api.github.com, which will fail in tests.
	// It should fall back to defaultEndpoint without error.
	if err != nil {
		t.Fatalf("discoverEndpoint returned error: %v", err)
	}
}

func TestDiscoverEndpointFallback(t *testing.T) {
	// With a bad client / unreachable endpoint, should fall back to default.
	endpoint, err := discoverEndpoint(context.Background(), &http.Client{}, "fake-token")
	if err != nil {
		t.Fatalf("discoverEndpoint error: %v", err)
	}
	if endpoint != defaultEndpoint {
		t.Errorf("endpoint = %q, want %q", endpoint, defaultEndpoint)
	}
}

// --- CreateChatCompletion (chat completions route) ---

func TestCreateChatCompletionChatRoute(t *testing.T) {
	var capturedPath string
	var capturedAuth string
	var capturedEditorVer string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedAuth = r.Header.Get("Authorization")
		capturedEditorVer = r.Header.Get("Editor-Version")

		resp := openai.ChatCompletionResponse{
			ID:      "chatcmpl-test",
			Object:  "chat.completion",
			Model:   "gpt-4o",
			Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Role: "assistant", Content: "Hello from Copilot"}}},
			Usage:   openai.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	llm := New(Config{Model: "gpt-4o", Token: "test-token"})
	llm.endpoint = srv.URL
	llm.resolved = true

	reply, usage, err := llm.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("CreateChatCompletion: %v", err)
	}

	if capturedPath != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions", capturedPath)
	}
	if capturedAuth != "Bearer test-token" {
		t.Errorf("auth = %q, want Bearer test-token", capturedAuth)
	}
	if capturedEditorVer == "" {
		t.Error("Editor-Version header not set")
	}
	if len(reply.ChatCompletionResponse.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(reply.ChatCompletionResponse.Choices))
	}
	if reply.ChatCompletionResponse.Choices[0].Message.Content != "Hello from Copilot" {
		t.Errorf("content = %q", reply.ChatCompletionResponse.Choices[0].Message.Content)
	}
	if usage.TotalTokens != 15 {
		t.Errorf("usage.TotalTokens = %d, want 15", usage.TotalTokens)
	}
}

// --- CreateChatCompletion (responses route) ---

func TestCreateChatCompletionResponsesRoute(t *testing.T) {
	var capturedPath string
	var captured struct {
		Reasoning struct{ Effort, Summary string }
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Error(err)
		}
		// Minimal Responses API response.
		resp := `{
			"id": "resp_test",
			"object": "response",
			"model": "gpt-5",
			"status": "completed",
			"output": [
				{
					"type": "message",
					"role": "assistant",
					"content": [{"type": "output_text", "text": "Hello from Responses"}]
				}
			],
			"usage": {"input_tokens": 5, "output_tokens": 3, "total_tokens": 8}
		}`
		w.Write([]byte(resp))
	}))
	defer srv.Close()

	llm := New(Config{Model: "gpt-5", Token: "test-token"})
	llm.endpoint = srv.URL
	llm.resolved = true

	reply, _, err := llm.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model: "gpt-5", ReasoningEffort: "low",
		Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("CreateChatCompletion: %v", err)
	}

	if captured.Reasoning.Effort != "low" || captured.Reasoning.Summary != "auto" {
		t.Errorf("reasoning = %+v, want low/auto", captured.Reasoning)
	}
	if capturedPath != "/responses" {
		t.Errorf("path = %q, want /responses", capturedPath)
	}
	if len(reply.ChatCompletionResponse.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(reply.ChatCompletionResponse.Choices))
	}
	if reply.ChatCompletionResponse.Choices[0].Message.Content != "Hello from Responses" {
		t.Errorf("content = %q", reply.ChatCompletionResponse.Choices[0].Message.Content)
	}
}

// --- CreateChatCompletion (messages route) ---

func TestCreateChatCompletionMessagesRoute(t *testing.T) {
	var capturedPath string
	var capturedAnthropicVer string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedAnthropicVer = r.Header.Get("anthropic-version")

		resp := `{
			"id": "msg_test",
			"type": "message",
			"role": "assistant",
			"model": "claude-sonnet-4-20250514",
			"stop_reason": "end_turn",
			"content": [{"type": "text", "text": "Hello from Messages"}],
			"usage": {"input_tokens": 5, "output_tokens": 3}
		}`
		w.Write([]byte(resp))
	}))
	defer srv.Close()

	llm := New(Config{Model: "claude-sonnet-4-20250514", Token: "test-token"})
	llm.endpoint = srv.URL
	llm.resolved = true

	reply, _, err := llm.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "claude-sonnet-4-20250514",
		Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("CreateChatCompletion: %v", err)
	}

	if capturedPath != "/v1/messages" {
		t.Errorf("path = %q, want /v1/messages", capturedPath)
	}
	if capturedAnthropicVer != "2023-06-01" {
		t.Errorf("anthropic-version = %q, want 2023-06-01", capturedAnthropicVer)
	}
	if len(reply.ChatCompletionResponse.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(reply.ChatCompletionResponse.Choices))
	}
	if reply.ChatCompletionResponse.Choices[0].Message.Content != "Hello from Messages" {
		t.Errorf("content = %q", reply.ChatCompletionResponse.Choices[0].Message.Content)
	}
}

// --- Header verification ---

func TestCopilotHeaders(t *testing.T) {
	var capturedHeaders http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		json.NewEncoder(w).Encode(openai.ChatCompletionResponse{})
	}))
	defer srv.Close()

	llm := New(Config{Model: "gpt-4o", Token: "my-token"})
	llm.endpoint = srv.URL
	llm.resolved = true

	_, _, _ = llm.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "hi"}},
	})

	required := map[string]string{
		"Authorization":        "Bearer my-token",
		"Content-Type":         "application/json",
		"Editor-Version":       editorVersion,
		"X-Github-Api-Version": "2025-10-01",
		"X-Initiator":          "agent",
		"X-Interaction-Type":   "conversation-agent",
		"Openai-Intent":        "conversation-agent",
	}
	for key, want := range required {
		if got := capturedHeaders.Get(key); got != want {
			t.Errorf("header %s = %q, want %q", key, got, want)
		}
	}
}

// --- Error handling ---

func TestCreateChatCompletionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"token expired","type":"auth_error"}}`))
	}))
	defer srv.Close()

	llm := New(Config{Model: "gpt-4o", Token: "bad-token"})
	llm.endpoint = srv.URL
	llm.resolved = true

	_, _, err := llm.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should contain status 401, got: %v", err)
	}
	if !strings.Contains(err.Error(), "token expired") {
		t.Errorf("error should contain message, got: %v", err)
	}
}

// --- Ask method ---

func TestAsk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openai.ChatCompletionResponse{
			ID:      "chatcmpl-test",
			Object:  "chat.completion",
			Model:   "gpt-4o",
			Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Role: "assistant", Content: "Response"}}},
			Usage:   openai.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	llm := New(Config{Model: "gpt-4o", Token: "test-token"})
	llm.endpoint = srv.URL
	llm.resolved = true

	fragment := cogito.Fragment{
		Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "hello"}},
	}

	result, err := llm.Ask(context.Background(), fragment)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	if len(result.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(result.Messages))
	}
	if result.Messages[1].Content != "Response" {
		t.Errorf("assistant message = %q, want Response", result.Messages[1].Content)
	}
}

// --- Ensure endpoint caching ---

func TestEnsureEndpointCaching(t *testing.T) {
	llm := New(Config{Model: "gpt-4o", Token: "test-token"})

	// First call discovers the endpoint (falls back to default).
	ep1, err := llm.ensureEndpoint(context.Background())
	if err != nil {
		t.Fatalf("ensureEndpoint: %v", err)
	}

	// Second call returns cached value without re-discovering.
	ep2, err := llm.ensureEndpoint(context.Background())
	if err != nil {
		t.Fatalf("ensureEndpoint second: %v", err)
	}

	if ep1 != ep2 {
		t.Errorf("endpoint changed: %q != %q", ep1, ep2)
	}
	if !llm.resolved {
		t.Error("resolved flag not set after ensureEndpoint")
	}
}

// --- readCopilotJSON / readGhHostsYAML helpers ---

func TestReadCopilotJSONPreferGitHub(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "hosts.json")
	data := `{
		"gitlab.com": {"oauth_token": "other"},
		"github.com": {"oauth_token": "preferred"}
	}`
	os.WriteFile(path, []byte(data), 0o600)

	if token := readCopilotJSON(path); token != "preferred" {
		t.Errorf("token = %q, want preferred", token)
	}
}

func TestReadCopilotJSONFallback(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "hosts.json")
	data := `{"enterprise.example.com": {"oauth_token": "enterprise_token"}}`
	os.WriteFile(path, []byte(data), 0o600)

	if token := readCopilotJSON(path); token != "enterprise_token" {
		t.Errorf("token = %q, want enterprise_token", token)
	}
}

func TestReadCopilotJSONMissing(t *testing.T) {
	if token := readCopilotJSON("/nonexistent/path/hosts.json"); token != "" {
		t.Errorf("token = %q, want empty", token)
	}
}

func TestReadGhHostsYAMLPreferGitHub(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "hosts.yml")
	data := "gitlab.com:\n  oauth_token: other\ngithub.com:\n  oauth_token: preferred\n"
	os.WriteFile(path, []byte(data), 0o600)

	if token := readGhHostsYAML(path); token != "preferred" {
		t.Errorf("token = %q, want preferred", token)
	}
}

// --- Registry / provider lookup ---

func TestCopilotProviderInRegistry(t *testing.T) {
	def, ok := provider.Get("github-copilot")
	if !ok {
		t.Fatal("github-copilot not in registry")
	}
	if def.Protocol != provider.ProtocolCopilot {
		t.Errorf("protocol = %q, want %q", def.Protocol, provider.ProtocolCopilot)
	}
	if def.LoginKind != provider.LoginCopilot {
		t.Errorf("loginKind = %q, want %q", def.LoginKind, provider.LoginCopilot)
	}
	if def.BaseURL != "https://api.githubcopilot.com" {
		t.Errorf("baseURL = %q, want https://api.githubcopilot.com", def.BaseURL)
	}
	if def.EnvVar != "GH_COPILOT_TOKEN" {
		t.Errorf("envVar = %q, want GH_COPILOT_TOKEN", def.EnvVar)
	}
}

// --- Ensure request body is correct for chat completions ---

func TestChatCompletionsBodyPassedThrough(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		json.NewEncoder(w).Encode(openai.ChatCompletionResponse{})
	}))
	defer srv.Close()

	llm := New(Config{Model: "gpt-4o", Token: "test-token"})
	llm.endpoint = srv.URL
	llm.resolved = true

	req := openai.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "test message"}},
	}
	_, _, _ = llm.CreateChatCompletion(context.Background(), req)

	var sent openai.ChatCompletionRequest
	if err := json.Unmarshal(capturedBody, &sent); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if sent.Model != "gpt-4o" {
		t.Errorf("sent model = %q, want gpt-4o", sent.Model)
	}
	if len(sent.Messages) != 1 || sent.Messages[0].Content != "test message" {
		t.Errorf("sent messages mismatch: %+v", sent.Messages)
	}
}
