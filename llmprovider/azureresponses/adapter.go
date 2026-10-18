// Package azureresponses adapts the Azure OpenAI Responses API to cogito.LLM.
//
// Azure uses the same Responses wire format as OpenAI but differs in:
//   - URL: {baseURL}/responses?api-version={version} (no /v1 prefix)
//   - Auth: api-key header (not Authorization: Bearer)
//   - Model: the deployment name (resolved from AZURE_OPENAI_DEPLOYMENT_NAME_MAP
//     or the model ID), not the model slug
//
// Request and response bodies are identical to the standard OpenAI Responses
// API, so translation is delegated to the openairesponses package.
package azureresponses

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/mudler/cogito"
	"github.com/mudler/nib/internal/fragmentreply"
	"github.com/mudler/nib/llmprovider/openairesponses"
	openai "github.com/sashabaranov/go-openai"
)

const defaultAPIVersion = "v1"

// Config holds the connection + auth parameters for the Azure adapter.
type Config struct {
	Model          string
	BaseURL        string // e.g. "https://{resource}.openai.azure.com/openai/v1"
	APIKey         string
	APIVersion     string // default "v1"
	DeploymentName string // if empty, Model is used
}

// LLM implements cogito.LLM against the Azure OpenAI Responses API.
type LLM struct {
	config Config
	client *http.Client
}

var _ cogito.LLM = (*LLM)(nil)

// New returns an Azure Responses adapter.
func New(config Config) *LLM {
	if config.APIVersion == "" {
		config.APIVersion = defaultAPIVersion
	}
	return &LLM{config: config, client: &http.Client{}}
}

// Ask delegates to CreateChatCompletion, mirroring the codexapp pattern.
func (l *LLM) Ask(ctx context.Context, fragment cogito.Fragment) (cogito.Fragment, error) {
	reply, usage, err := l.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    l.config.Model,
		Messages: fragment.GetMessages(),
	})
	if err != nil {
		return fragment, err
	}
	if len(reply.ChatCompletionResponse.Choices) == 0 {
		return fragment, openairesponses.ErrNoResponseSentinel
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

// CreateChatCompletion translates an OpenAI Chat Completion request to the
// Azure Responses API, calls it, and translates the response back.
func (l *LLM) CreateChatCompletion(ctx context.Context, request openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	// Override model with deployment name for the request body.
	deploymentModel := l.config.DeploymentName
	if deploymentModel == "" {
		deploymentModel = firstNonEmpty(request.Model, l.config.Model)
	}
	request.Model = deploymentModel

	body, err := openairesponses.TranslateRequest(request)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("azure-responses: translate request: %w", err)
	}

	url := strings.TrimRight(l.config.BaseURL, "/") + "/responses?api-version=" + l.config.APIVersion

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("azure-responses: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("api-key", l.config.APIKey)

	resp, err := l.client.Do(req)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("azure-responses: request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("azure-responses: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return cogito.LLMReply{}, cogito.LLMUsage{}, fmt.Errorf("azure-responses: %w", openairesponses.ParseAPIError(resp.StatusCode, respBody))
	}

	return openairesponses.TranslateResponse(respBody, request.Model)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// resolveBaseURL determines the Azure endpoint from config, env, or resource name.
func resolveBaseURL(configured string) string {
	if configured != "" {
		return strings.TrimRight(configured, "/")
	}
	if v := os.Getenv("AZURE_OPENAI_BASE_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	if rn := os.Getenv("AZURE_OPENAI_RESOURCE_NAME"); rn != "" {
		return "https://" + rn + ".openai.azure.com/openai/v1"
	}
	return ""
}

// resolveDeploymentName maps a model ID to an Azure deployment name.
func resolveDeploymentName(modelID string) string {
	for _, kv := range deploymentMap() {
		if kv[0] == modelID {
			return kv[1]
		}
	}
	return modelID
}

// deploymentMap parses AZURE_OPENAI_DEPLOYMENT_NAME_MAP ("model=deployment,…")
// into ordered [model, deployment] pairs.
func deploymentMap() [][2]string {
	var out [][2]string
	for _, pair := range strings.Split(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP"), ",") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 && strings.TrimSpace(kv[0]) != "" {
			out = append(out, [2]string{strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])})
		}
	}
	return out
}
