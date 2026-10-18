// Package googlevertex adapts the Google Vertex AI API to cogito.LLM.
//
// Vertex AI uses the same Gemini generateContent body format as Google AI
// Studio (package google), but with a different URL structure that includes
// the project and location, and different auth (API key via x-goog-api-key
// header, or OAuth Bearer token).
//
// The request/response translation is delegated to the google package's
// exported TranslateRequest/TranslateResponse functions.
package googlevertex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/internal/fragmentreply"
	"github.com/mudler/nib/llmprovider/google"
	openai "github.com/sashabaranov/go-openai"
)

const defaultLocation = "global"

// Config holds the connection + auth parameters for the Vertex AI adapter.
type Config struct {
	Model    string
	BaseURL  string // default "https://aiplatform.googleapis.com"
	APIKey   string // for x-goog-api-key auth
	Token    string // for Bearer auth (OAuth)
	IsOAuth  bool
	Project  string // required for OAuth auth
	Location string // default "global"
}

// LLM implements cogito.LLM against the Vertex AI API.
type LLM struct {
	config Config
	client *http.Client
}

var _ cogito.LLM = (*LLM)(nil)

// New returns a Vertex AI adapter. If BaseURL is empty, defaults to
// https://aiplatform.googleapis.com. If Location is empty, defaults to "global".
func New(config Config) *LLM {
	if config.BaseURL == "" {
		config.BaseURL = "https://aiplatform.googleapis.com"
	}
	if config.Location == "" {
		config.Location = defaultLocation
	}
	return &LLM{config: config, client: &http.Client{}}
}

// Ask delegates to CreateChatCompletion, mirroring the google adapter pattern.
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

// ErrNoResponse is returned when the API returns a candidate with no content.
var ErrNoResponse = errors.New("google-vertex: completed without an assistant message")

// CreateChatCompletion translates an OpenAI Chat Completion request to the
// Vertex AI API, calls it, and translates the response back.
func (l *LLM) CreateChatCompletion(ctx context.Context, request openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	body, err := google.TranslateRequest(request)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("google-vertex: %w", err)
	}

	model := firstNonEmpty(request.Model, l.config.Model)
	url := l.buildURL(model)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("google-vertex: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if l.config.IsOAuth {
		req.Header.Set("Authorization", "Bearer "+l.config.Token)
	} else {
		req.Header.Set("x-goog-api-key", l.config.APIKey)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("google-vertex: request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("google-vertex: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return cogito.LLMReply{}, cogito.LLMUsage{}, parseAPIError(resp.StatusCode, respBody)
	}

	return google.TranslateResponse(respBody, model)
}

// buildURL constructs the Vertex AI endpoint URL. For OAuth auth, the URL
// includes the project and location. For API key auth, the URL is shorter
// (the key identifies the project).
func (l *LLM) buildURL(model string) string {
	base := strings.TrimRight(l.config.BaseURL, "/")
	if l.config.IsOAuth {
		return fmt.Sprintf("%s/v1/projects/%s/locations/%s/publishers/google/models/%s:generateContent",
			base, l.config.Project, l.config.Location, model)
	}
	return fmt.Sprintf("%s/v1/publishers/google/models/%s:generateContent", base, model)
}

// ---------------------------------------------------------------------------
// Error handling
// ---------------------------------------------------------------------------

type vertexErrorResp struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

func parseAPIError(status int, body []byte) error {
	var ve vertexErrorResp
	if err := json.Unmarshal(body, &ve); err == nil && ve.Error.Message != "" {
		return fmt.Errorf("google-vertex: %d %s: %s", status, ve.Error.Status, ve.Error.Message)
	}
	return fmt.Errorf("google-vertex: HTTP %d: %s", status, strings.TrimSpace(string(body)))
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
