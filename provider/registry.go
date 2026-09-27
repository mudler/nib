// Package provider holds the static registry of remote LLM providers nib can
// route to: their wire protocol, default base URL, environment variable, and
// (for OAuth-backed providers) the OAuth flow parameters.
//
// The registry is the single source of truth for "which providers exist and
// how do I reach them". Credential storage and resolution live in package
// auth; wire-protocol adapters live in package llmprovider and its
// subpackages.
package provider

// Protocol is the wire format a provider speaks. It lives on the model, not
// the provider — but every built-in provider has a default protocol its
// models use unless overridden.
type Protocol string

const (
	// ProtocolOpenAICompletions is the OpenAI Chat Completions API
	// (/v1/chat/completions). Any OpenAI-compatible endpoint uses this:
	// OpenAI itself, LocalAI, Ollama, vLLM, Groq, Together, OpenRouter, etc.
	ProtocolOpenAICompletions Protocol = "openai-completions"
	// ProtocolAnthropicMessages is the Anthropic Messages API (/v1/messages).
	ProtocolAnthropicMessages Protocol = "anthropic-messages"
	// ProtocolGoogleGemini is the Google Generative AI API
	// (/v1beta/models/{model}:generateContent). Used by Google AI Studio
	// and Gemini CLI.
	ProtocolGoogleGemini Protocol = "google-generative-ai"
	// ProtocolOpenAIResponses is the OpenAI Responses API
	// (/v1/responses). Used by OpenAI, GitHub Copilot, xAI OAuth,
	// Muse Code, Meta, and others.
	ProtocolOpenAIResponses Protocol = "openai-responses"
	// ProtocolOllamaChat is the Ollama native chat API (/api/chat).
	// Used by local Ollama and Ollama Cloud.
	ProtocolOllamaChat Protocol = "ollama-chat"
	// ProtocolGeminiCLI is the Google Cloud Code Assist API
	// (/v1internal:generateContent). Used by Gemini CLI. Same Gemini body
	// format as ProtocolGoogleGemini but wrapped in a Cloud Code Assist
	// envelope {project, model, request} and requiring a project ID.
	ProtocolGeminiCLI Protocol = "google-gemini-cli"
	// ProtocolGoogleVertex is the Google Vertex AI API
	// (/v1/projects/{project}/locations/{location}/publishers/google/models/{model}:generateContent).
	// Same Gemini body format as ProtocolGoogleGemini but with Vertex AI
	// URL structure and auth (API key or OAuth Bearer).
	ProtocolGoogleVertex Protocol = "google-vertex"
	// ProtocolBedrockConverse is the AWS Bedrock Converse Stream API
	// (/model/{model}/converse-stream). Uses AWS SigV4 signing and
	// binary eventstream response decoding.
	ProtocolBedrockConverse Protocol = "bedrock-converse-stream"
	// ProtocolAzureResponses is the Azure OpenAI Responses API
	// (/responses?api-version={version}). Same wire format as
	// ProtocolOpenAIResponses but with Azure auth (api-key header),
	// deployment-name model mapping, and api-version query param.
	ProtocolAzureResponses Protocol = "azure-openai-responses"
	// ProtocolCodexResponses is the OpenAI Codex Responses API
	// (/codex/responses). Same wire format as ProtocolOpenAIResponses
	// but targets the ChatGPT backend, requires SSE streaming, rejects
	// sampling params, and carries Codex identity headers (originator,
	// version, chatgpt-account-id from JWT).
	ProtocolCodexResponses Protocol = "openai-codex-responses"
	// ProtocolCopilot is the GitHub Copilot multi-protocol adapter. It
	// routes each request to OpenAI Chat Completions, OpenAI Responses, or
	// Anthropic Messages based on the model, and requires a Copilot
	// token imported from the gh CLI / Copilot client.
	ProtocolCopilot Protocol = "github-copilot"
)

// LoginKind classifies how a provider authenticates.
type LoginKind string

const (
	// LoginNone means the provider has no interactive login flow; credentials
	// come from the environment or config only (e.g. a bare OPENAI_API_KEY,
	// or a local Ollama instance that needs no key).
	LoginNone LoginKind = ""
	// LoginAPIKey means /login prompts for an API key and stores it.
	LoginAPIKey LoginKind = "api-key"
	// LoginOAuthCode means /login runs an OAuth authorization-code flow with
	// PKCE and a loopback callback server.
	LoginOAuthCode LoginKind = "oauth-code"
	// LoginDeviceCode means /login runs an RFC 8628 device-code flow:
	// the user visits a URL and enters a code in a browser. Not yet
	// implemented — reserved for Phase 3.
	LoginDeviceCode LoginKind = "device-code"
	// LoginCopilot means /login imports an existing Copilot token from
	// the gh CLI or Copilot client config (env vars, ~/.config/github-copilot/,
	// ~/.config/gh/) and stores it. There is no OAuth or device-code flow —
	// the user authenticates via gh CLI first.
	LoginCopilot LoginKind = "copilot-token"
)

// Definition describes one remote provider: how to reach it and how to log in.
type Definition struct {
	ID   string
	Name string // display name
	// Protocol is the default wire protocol for this provider's models.
	Protocol Protocol
	// BaseURL is the default API base URL (no trailing slash). Empty means
	// the OpenAI SDK default.
	BaseURL string
	// EnvVar is the environment variable that holds an API key fallback.
	EnvVar string
	// LoginKind selects the /login flow. LoginNone = env/config only.
	LoginKind LoginKind
	// CallbackPort is the loopback port the OAuth callback server binds.
	// 0 for non-OAuth providers.
	CallbackPort int
	// CallbackPath is the URL path the OAuth callback server registers.
	// Defaults to "/callback" if empty. Some providers require a specific
	// path (e.g. Google uses "/oauth2callback", OpenAI uses "/auth/callback").
	CallbackPath string
	// CallbackHost is the hostname used in the redirect URI. Defaults to
	// "localhost" if empty. Some providers require "127.0.0.1".
	CallbackHost string
	// AllowPortFallback allows the callback server to bind a random port if
	// the preferred one is busy. False for providers that validate the
	// redirect URI.
	AllowPortFallback bool

	// OAuth authorization-code flow parameters (LoginOAuthCode only).
	ClientID        string
	AuthorizeURL    string
	TokenURL        string
	Scopes          []string
	AuthorizeParams map[string]string

	// EnvClientID, if set, names an environment variable that overrides
	// ClientID at runtime. Used to avoid hardlisting OAuth client
	// credentials in source.
	EnvClientID string
	// EnvClientSecret works like EnvClientID for ClientSecret.
	EnvClientSecret string

	// DeviceURL is the device authorization endpoint (RFC 8628). Used by
	// LoginDeviceCode providers; the token endpoint is still TokenURL.
	DeviceURL string

	// TokenBodyFormat is "json" or "form". Controls how the token
	// endpoint request body is encoded. Default "json" (empty = json).
	TokenBodyFormat string
	// ClientSecret is sent in token and refresh requests if non-empty.
	// Required by providers like Google that use a confidential client.
	ClientSecret string
	// ExtraTokenHeaders are added to every token and refresh request.
	// Used for provider-specific headers like Anthropic's anthropic-beta.
	ExtraTokenHeaders map[string]string

	// IdentityURL is the endpoint called after a successful token exchange
	// to recover account info (email, account ID). Empty = skip identity
	// recovery; the credential is still valid.
	IdentityURL string
	// IdentityMethod controls how IdentityURL is called:
	// "bootstrap" — Anthropic's claude_cli/bootstrap (custom JSON shape)
	// "userinfo"  — standard OIDC userinfo endpoint (email/sub fields)
	// Empty = skip (same as IdentityURL being empty).
	IdentityMethod string
}

// providerOrder is the stable iteration order for All() and Loginable().
// Providers that users are most likely to log in to come first.
var providerOrder = []string{
	// OAuth providers
	"anthropic",
	"openai-codex",
	"google-gemini-cli",
	"google-antigravity",
	"xai-oauth",
	"kimi-code",
	"muse-code",
	"github-copilot",
	// API-key cloud providers (most popular first, then alphabetical)
	"openai",
	"groq",
	"together",
	"mistral",
	"fireworks",
	"deepseek",
	"perplexity",
	"openrouter",
	"moonshot",
	"xai",
	"zai",
	"cerebras",
	"nvidia",
	"deepinfra",
	"huggingface",
	"novita",
	"siliconflow",
	"siliconflow-cn",
	"venice",
	"baseten",
	"aimlapi",
	"sakana",
	"nanogpt",
	"gmi-cloud",
	"minimax",
	"minimax-code",
	"minimax-code-cn",
	"qianfan",
	"qwen-portal",
	"umans",
	"zhipu-coding-plan",
	"meta",
	"yolo-auto",
	"vercel-ai-gateway",
	"coreweave",
	"abliteration",
	"aiand",
	"synthetic",
	"wafer-serverless",
	"charm-hyper",
	"cline-pass",
	"commandcode",
	"firepass",
	"kilo",
	"zenmux",
	"opencode-zen",
	"opencode-go",
	"ollama-cloud",
	"xiaomi",
	"xiaomi-token-plan-sgp",
	"xiaomi-token-plan-ams",
	"xiaomi-token-plan-cn",
	"alibaba-coding-plan",
	"alibaba-token-plan",
	// Additional OpenAI-compatible providers
	"requesty",
	"regolo",
	"tensorx",
	"hyperbolic",
	"sambanova",
	"chutes",
	"glhf",
	"reka",
	"scaleway",
	"inference-net",
	"nebius",
	"ai21",
	"nscale",
	"kluster",
	"lambda",
	"friendli",
	"cohere",
	"ovhcloud",
	"modelscope",
	"clarifai",
	"arcee",
	"poolside",
	"llm7",
	// Local providers (no interactive login)
	"ollama",
	"llama.cpp",
	"google",
	"google-vertex",
	"amazon-bedrock",
	"azure",
	"lm-studio",
	"vllm",
	"litellm",
}

var registry = map[string]Definition{
	// -----------------------------------------------------------------------
	// OAuth authorization-code providers
	// -----------------------------------------------------------------------
	"anthropic": {
		ID:                "anthropic",
		Name:              "Anthropic (Claude Pro/Max)",
		Protocol:          ProtocolAnthropicMessages,
		BaseURL:           "https://api.anthropic.com",
		EnvVar:            "ANTHROPIC_API_KEY",
		LoginKind:         LoginOAuthCode,
		CallbackPort:      54545,
		AllowPortFallback: false,
		ClientID:          "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
		AuthorizeURL:      "https://claude.ai/oauth/authorize",
		TokenURL:          "https://api.anthropic.com/v1/oauth/token",
		Scopes: []string{
			"org:create_api_key",
			"user:profile",
			"user:inference",
			"user:sessions:claude_code",
			"user:mcp_servers",
			"user:file_upload",
		},
		AuthorizeParams:   map[string]string{"code": "true"},
		ExtraTokenHeaders: map[string]string{"anthropic-beta": "oauth-2025-04-20"},
		IdentityURL:       "https://api.anthropic.com/api/claude_cli/bootstrap?entrypoint=cli",
		IdentityMethod:    "bootstrap",
	},

	// -----------------------------------------------------------------------
	// OAuth authorization-code providers (non-Anthropic)
	// -----------------------------------------------------------------------
	"openai-codex": {
		ID:                "openai-codex",
		Name:              "ChatGPT Plus/Pro (Codex Subscription)",
		Protocol:          ProtocolCodexResponses,
		BaseURL:           "",
		EnvVar:            "OPENAI_CODEX_OAUTH_TOKEN",
		LoginKind:         LoginOAuthCode,
		CallbackPort:      1455,
		CallbackPath:      "/auth/callback",
		CallbackHost:      "127.0.0.1",
		AllowPortFallback: false,
		ClientID:          "app_EMoamEEZ73f0CkXaXp7hrann",
		AuthorizeURL:      "https://auth.openai.com/oauth/authorize",
		TokenURL:          "https://auth.openai.com/oauth/token",
		TokenBodyFormat:   "form",
		// NOTE: OpenAI's device-code flow is non-standard (custom JSON
		// endpoints at /api/accounts/deviceauth/*, two-step code+PKCE
		// exchange, 403/404 as "pending" signal). It cannot be served by
		// the RFC 8628 implementation in auth/oauth/device.go. Do NOT set
		// DeviceURL here until a custom device-flow handler is added.
		// Over SSH, the port-forward hint + paste-the-code fallback covers
		// openai-codex.
		Scopes: []string{
			"openid", "profile", "email", "offline_access",
			"api.connectors.read", "api.connectors.invoke",
		},
		AuthorizeParams: map[string]string{
			"id_token_add_organizations": "true",
			"codex_cli_simplified_flow":  "true",
			"originator":                 "nib",
		},
	},
	"google-gemini-cli": {
		ID:                "google-gemini-cli",
		Name:              "Google Cloud Code Assist (Gemini CLI)",
		Protocol:          ProtocolGeminiCLI,
		BaseURL:           "https://cloudcode-pa.googleapis.com",
		EnvVar:            "GOOGLE_API_KEY",
		LoginKind:         LoginOAuthCode,
		CallbackPort:      8085,
		CallbackPath:      "/oauth2callback",
		CallbackHost:      "127.0.0.1",
		AllowPortFallback: false,
		EnvClientID:       "GEMINI_CLI_OAUTH_CLIENT_ID",
		EnvClientSecret:   "GEMINI_CLI_OAUTH_CLIENT_SECRET",
		AuthorizeURL:      "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:          "https://oauth2.googleapis.com/token",
		TokenBodyFormat:   "form",
		// DeviceURL enables `nib login google-gemini-cli --device` (RFC 8628).
		// Google's OAuth provider supports the device-code grant, which avoids
		// the localhost callback entirely — ideal for SSH sessions.
		DeviceURL:         "https://oauth2.googleapis.com/device/code",
		Scopes: []string{
			"https://www.googleapis.com/auth/cloud-platform",
			"https://www.googleapis.com/auth/userinfo.email",
			"https://www.googleapis.com/auth/userinfo.profile",
		},
		AuthorizeParams: map[string]string{
			"access_type": "offline",
			"prompt":      "consent",
		},
		IdentityURL:    "https://www.googleapis.com/oauth2/v1/userinfo?alt=json",
		IdentityMethod: "userinfo",
	},
	"google-antigravity": {
		ID:                "google-antigravity",
		Name:              "Antigravity (Gemini 3, Claude, GPT-OSS)",
		Protocol:          ProtocolGoogleGemini,
		BaseURL:           "https://generativelanguage.googleapis.com/v1beta",
		EnvVar:            "GOOGLE_API_KEY",
		LoginKind:         LoginOAuthCode,
		CallbackPort:      51121,
		CallbackPath:      "/oauth-callback",
		CallbackHost:      "127.0.0.1",
		AllowPortFallback: false,
		EnvClientID:       "ANTIGRAVITY_OAUTH_CLIENT_ID",
		EnvClientSecret:   "ANTIGRAVITY_OAUTH_CLIENT_SECRET",
		AuthorizeURL:      "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:          "https://oauth2.googleapis.com/token",
		TokenBodyFormat:   "form",
		// DeviceURL enables `nib login google-antigravity --device` (RFC 8628).
		DeviceURL:         "https://oauth2.googleapis.com/device/code",
		Scopes: []string{
			"https://www.googleapis.com/auth/cloud-platform",
			"https://www.googleapis.com/auth/userinfo.email",
			"https://www.googleapis.com/auth/userinfo.profile",
			"https://www.googleapis.com/auth/cclog",
			"https://www.googleapis.com/auth/experimentsandconfigs",
		},
		AuthorizeParams: map[string]string{
			"access_type": "offline",
			"prompt":      "consent",
		},
		IdentityURL:    "https://www.googleapis.com/oauth2/v1/userinfo?alt=json",
		IdentityMethod: "userinfo",
	},

	// -----------------------------------------------------------------------
	// Device-code providers (RFC 8628)
	// -----------------------------------------------------------------------
	"xai-oauth": {
		ID:              "xai-oauth",
		Name:            "xAI Grok OAuth (SuperGrok or X Premium+)",
		Protocol:        ProtocolOpenAICompletions,
		BaseURL:         "https://api.x.ai/v1",
		EnvVar:          "XAI_API_KEY",
		LoginKind:       LoginDeviceCode,
		ClientID:        "b1a00492-073a-47ea-816f-4c329264a828",
		TokenURL:        "https://auth.x.ai/oauth2/token",
		DeviceURL:       "https://auth.x.ai/oauth2/device/code",
		TokenBodyFormat: "form",
		Scopes: []string{
			"openid", "profile", "email", "offline_access",
			"grok-cli:access", "api:access",
		},
		IdentityURL:    "https://auth.x.ai/oauth2/userinfo",
		IdentityMethod: "userinfo",
	},
	"kimi-code": {
		ID:              "kimi-code",
		Name:            "Kimi Code",
		Protocol:        ProtocolOpenAICompletions,
		BaseURL:         "https://api.kimi.com/coding/v1",
		EnvVar:          "KIMI_API_KEY",
		LoginKind:       LoginDeviceCode,
		ClientID:        "17e5f671-d194-4dfb-9706-5516cb48c098",
		TokenURL:        "https://auth.kimi.com/api/oauth/token",
		DeviceURL:       "https://auth.kimi.com/api/oauth/device_authorization",
		TokenBodyFormat: "form",
	},
	"muse-code": {
		ID:              "muse-code",
		Name:            "Muse Code (Subscription)",
		Protocol:        ProtocolOpenAICompletions,
		BaseURL:         "https://api.meta.ai/v1",
		EnvVar:          "",
		LoginKind:       LoginDeviceCode,
		ClientID:        "1031625952748946",
		TokenURL:        "https://auth.meta.com/oidc/device/token/",
		DeviceURL:       "https://auth.meta.com/oidc/device/authorization/",
		TokenBodyFormat: "form",
		ExtraTokenHeaders: map[string]string{
			"x-api-version": "1.0.0",
		},
	},

	// -----------------------------------------------------------------------
	// GitHub Copilot (token-import provider)
	// -----------------------------------------------------------------------
	"github-copilot": {
		ID:        "github-copilot",
		Name:      "GitHub Copilot",
		Protocol:  ProtocolCopilot,
		BaseURL:   "https://api.githubcopilot.com",
		EnvVar:    "GH_COPILOT_TOKEN",
		LoginKind: LoginCopilot,
	},

	// -----------------------------------------------------------------------
	// OpenAI-compatible API-key providers (cloud)
	// -----------------------------------------------------------------------
	"openai": {
		ID:        "openai",
		Name:      "OpenAI",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "", // OpenAI SDK default
		EnvVar:    "OPENAI_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"groq": {
		ID:        "groq",
		Name:      "Groq",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.groq.com/openai/v1",
		EnvVar:    "GROQ_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"together": {
		ID:        "together",
		Name:      "Together AI",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.together.xyz/v1",
		EnvVar:    "TOGETHER_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"mistral": {
		ID:        "mistral",
		Name:      "Mistral",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.mistral.ai/v1",
		EnvVar:    "MISTRAL_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"fireworks": {
		ID:        "fireworks",
		Name:      "Fireworks",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.fireworks.ai/inference/v1",
		EnvVar:    "FIREWORKS_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"deepseek": {
		ID:        "deepseek",
		Name:      "DeepSeek",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.deepseek.com",
		EnvVar:    "DEEPSEEK_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"perplexity": {
		ID:        "perplexity",
		Name:      "Perplexity",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.perplexity.ai",
		EnvVar:    "PERPLEXITY_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"openrouter": {
		ID:        "openrouter",
		Name:      "OpenRouter",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://openrouter.ai/api/v1",
		EnvVar:    "OPENROUTER_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"moonshot": {
		ID:        "moonshot",
		Name:      "Moonshot (Kimi API)",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.moonshot.ai/v1",
		EnvVar:    "MOONSHOT_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"xai": {
		ID:        "xai",
		Name:      "xAI",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.x.ai/v1",
		EnvVar:    "XAI_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"cerebras": {
		ID:        "cerebras",
		Name:      "Cerebras",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.cerebras.ai/v1",
		EnvVar:    "CEREBRAS_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"nvidia": {
		ID:        "nvidia",
		Name:      "NVIDIA",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://integrate.api.nvidia.com/v1",
		EnvVar:    "NVIDIA_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"deepinfra": {
		ID:        "deepinfra",
		Name:      "DeepInfra",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.deepinfra.com/v1/openai",
		EnvVar:    "DEEPINFRA_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"huggingface": {
		ID:        "huggingface",
		Name:      "Hugging Face Inference",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://router.huggingface.co/v1",
		EnvVar:    "HUGGINGFACE_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"novita": {
		ID:        "novita",
		Name:      "Novita",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.novita.ai/openai/v1",
		EnvVar:    "NOVITA_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"siliconflow": {
		ID:        "siliconflow",
		Name:      "SiliconFlow",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.siliconflow.com/v1",
		EnvVar:    "SILICONFLOW_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"siliconflow-cn": {
		ID:        "siliconflow-cn",
		Name:      "SiliconFlow (China)",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.siliconflow.cn/v1",
		EnvVar:    "SILICONFLOW_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"venice": {
		ID:        "venice",
		Name:      "Venice",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.venice.ai/api/v1",
		EnvVar:    "VENICE_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"baseten": {
		ID:        "baseten",
		Name:      "Baseten",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://inference.baseten.co/v1",
		EnvVar:    "BASETEN_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"aimlapi": {
		ID:        "aimlapi",
		Name:      "AIML API",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.aimlapi.com/v1",
		EnvVar:    "AIMLAPI_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"sakana": {
		ID:        "sakana",
		Name:      "Sakana AI",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.sakana.ai/v1",
		EnvVar:    "SAKANA_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"nanogpt": {
		ID:        "nanogpt",
		Name:      "NanoGPT",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://nano-gpt.com/api/v1",
		EnvVar:    "NANOGPT_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"gmi-cloud": {
		ID:        "gmi-cloud",
		Name:      "GMI Cloud",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.gmi-serving.com/v1",
		EnvVar:    "GMI_CLOUD_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"minimax": {
		ID:        "minimax",
		Name:      "MiniMax",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.minimax.io/v1",
		EnvVar:    "MINIMAX_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"qianfan": {
		ID:        "qianfan",
		Name:      "Qianfan",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://qianfan.baidubce.com/v2",
		EnvVar:    "QIANFAN_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"qwen-portal": {
		ID:        "qwen-portal",
		Name:      "Qwen Portal",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://portal.qwen.ai/v1",
		EnvVar:    "QWEN_PORTAL_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"meta": {
		ID:        "meta",
		Name:      "Meta Model API",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.meta.ai/v1",
		EnvVar:    "META_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"yolo-auto": {
		ID:        "yolo-auto",
		Name:      "Yolo-Auto",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://yolo-auto.com/v1",
		EnvVar:    "YOLO_AUTO_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"vercel-ai-gateway": {
		ID:        "vercel-ai-gateway",
		Name:      "Vercel AI Gateway",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://ai-gateway.vercel.sh",
		EnvVar:    "AI_GATEWAY_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"coreweave": {
		ID:        "coreweave",
		Name:      "CoreWeave Serverless Inference",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.inference.wandb.ai/v1",
		EnvVar:    "COREWEAVE_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"abliteration": {
		ID:        "abliteration",
		Name:      "Abliteration",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.abliteration.ai/v1",
		EnvVar:    "ABLITERATION_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"aiand": {
		ID:        "aiand",
		Name:      "ai&",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.aiand.com/v1",
		EnvVar:    "AIAND_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"synthetic": {
		ID:        "synthetic",
		Name:      "Synthetic",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.synthetic.new/openai/v1",
		EnvVar:    "SYNTHETIC_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"wafer-serverless": {
		ID:        "wafer-serverless",
		Name:      "Wafer Serverless",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://pass.wafer.ai/v1",
		EnvVar:    "WAFER_SERVERLESS_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"charm-hyper": {
		ID:        "charm-hyper",
		Name:      "Charm Hyper",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://hyper.charm.land/v1",
		EnvVar:    "CHARM_HYPER_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"cline-pass": {
		ID:        "cline-pass",
		Name:      "ClinePass",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.cline.bot/api/v1",
		EnvVar:    "CLINE_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"commandcode": {
		ID:        "commandcode",
		Name:      "Command Code",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.commandcode.ai/provider",
		EnvVar:    "COMMAND_CODE_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"firepass": {
		ID:        "firepass",
		Name:      "Fire Pass (Fireworks subscription)",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.fireworks.ai/inference/v1",
		EnvVar:    "FIREPASS_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"kilo": {
		ID:        "kilo",
		Name:      "Kilo Gateway",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.kilo.ai/api/gateway",
		EnvVar:    "KILO_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"zenmux": {
		ID:        "zenmux",
		Name:      "ZenMux",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://zenmux.ai/api/v1",
		EnvVar:    "ZENMUX_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"opencode-zen": {
		ID:        "opencode-zen",
		Name:      "OpenCode Zen",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://opencode.ai/zen/v1",
		EnvVar:    "OPENCODE_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"opencode-go": {
		ID:        "opencode-go",
		Name:      "OpenCode Go",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://opencode.ai/zen/go/v1",
		EnvVar:    "OPENCODE_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"ollama-cloud": {
		ID:        "ollama-cloud",
		Name:      "Ollama Cloud",
		Protocol:  ProtocolOllamaChat,
		BaseURL:   "https://ollama.com",
		EnvVar:    "OLLAMA_CLOUD_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"zhipu-coding-plan": {
		ID:        "zhipu-coding-plan",
		Name:      "Zhipu Coding Plan",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://open.bigmodel.cn/api/coding/paas/v4",
		EnvVar:    "ZHIPU_API_KEY",
		LoginKind: LoginAPIKey,
	},

	// -----------------------------------------------------------------------
	// Additional OpenAI-compatible providers
	// -----------------------------------------------------------------------
	"requesty": {
		ID:        "requesty",
		Name:      "Requesty",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://router.requesty.ai/v1",
		EnvVar:    "REQUESTY_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"regolo": {
		ID:        "regolo",
		Name:      "Regolo",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.regolo.ai/v1",
		EnvVar:    "REGOLO_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"tensorx": {
		ID:        "tensorx",
		Name:      "TensorX",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.tensorx.ai/v1",
		EnvVar:    "TENSORX_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"hyperbolic": {
		ID:        "hyperbolic",
		Name:      "Hyperbolic",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.hyperbolic.xyz/v1",
		EnvVar:    "HYPERBOLIC_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"sambanova": {
		ID:        "sambanova",
		Name:      "SambaNova",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.sambanova.ai/v1",
		EnvVar:    "SAMBANOVA_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"chutes": {
		ID:        "chutes",
		Name:      "Chutes.ai",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.chutes.ai/v1",
		EnvVar:    "CHUTES_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"glhf": {
		ID:        "glhf",
		Name:      "Glhf.chat",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://glhf.chat/api/openai/v1",
		EnvVar:    "GLHF_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"reka": {
		ID:        "reka",
		Name:      "Reka AI",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.reka.ai/v1",
		EnvVar:    "REKA_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"scaleway": {
		ID:        "scaleway",
		Name:      "Scaleway Generative APIs",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.scaleway.ai/v1",
		EnvVar:    "SCALEWAY_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"inference-net": {
		ID:        "inference-net",
		Name:      "Inference.net",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.inference.net/v1",
		EnvVar:    "INFERENCE_NET_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"nebius": {
		ID:        "nebius",
		Name:      "Nebius AI Studio",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.studio.nebius.com/v1",
		EnvVar:    "NEBIUS_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"ai21": {
		ID:        "ai21",
		Name:      "AI21 Labs",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.ai21.com/studio/v1",
		EnvVar:    "AI21_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"nscale": {
		ID:        "nscale",
		Name:      "Nscale",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://inference.api.nscale.com/v1",
		EnvVar:    "NSCALE_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"kluster": {
		ID:        "kluster",
		Name:      "Kluster AI",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.kluster.ai/v1",
		EnvVar:    "KLUSTER_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"lambda": {
		ID:        "lambda",
		Name:      "Lambda AI",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.lambda.ai/v1",
		EnvVar:    "LAMBDA_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"friendli": {
		ID:        "friendli",
		Name:      "Friendli",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.friendli.ai/v1",
		EnvVar:    "FRIENDLI_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"cohere": {
		ID:        "cohere",
		Name:      "Cohere (Compatibility API)",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.cohere.com/compatibility/v1",
		EnvVar:    "COHERE_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"ovhcloud": {
		ID:        "ovhcloud",
		Name:      "OVHcloud AI Endpoints",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://oai.endpoints.kepler.ai.cloud.ovh.net/v1",
		EnvVar:    "OVHCLOUD_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"modelscope": {
		ID:        "modelscope",
		Name:      "ModelScope",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api-inference.modelscope.cn/v1",
		EnvVar:    "MODELSCOPE_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"clarifai": {
		ID:        "clarifai",
		Name:      "Clarifai",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.clarifai.com/v2/ext/openai/v1",
		EnvVar:    "CLARIFAI_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"arcee": {
		ID:        "arcee",
		Name:      "Arcee AI",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://conductor.arcee.ai/v1",
		EnvVar:    "ARCEE_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"poolside": {
		ID:        "poolside",
		Name:      "Poolside",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://inference.poolside.ai/v1",
		EnvVar:    "POOLSIDE_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"llm7": {
		ID:        "llm7",
		Name:      "LLM7.io",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.llm7.io/v1",
		EnvVar:    "LLM7_API_KEY",
		LoginKind: LoginNone,
	},

	// -----------------------------------------------------------------------
	// Anthropic-compatible API-key providers
	// -----------------------------------------------------------------------
	"zai": {
		ID:        "zai",
		Name:      "Z.AI (GLM)",
		Protocol:  ProtocolAnthropicMessages,
		BaseURL:   "https://api.z.ai",
		EnvVar:    "ZAI_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"umans": {
		ID:        "umans",
		Name:      "Umans AI Coding Plan",
		Protocol:  ProtocolAnthropicMessages,
		BaseURL:   "https://api.code.umans.ai",
		EnvVar:    "UMANS_AI_CODING_PLAN_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"minimax-code": {
		ID:        "minimax-code",
		Name:      "MiniMax (Anthropic API)",
		Protocol:  ProtocolAnthropicMessages,
		BaseURL:   "https://api.minimax.io",
		EnvVar:    "MINIMAX_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"minimax-code-cn": {
		ID:        "minimax-code-cn",
		Name:      "MiniMax China (Anthropic API)",
		Protocol:  ProtocolAnthropicMessages,
		BaseURL:   "https://api.minimaxi.com",
		EnvVar:    "MINIMAX_API_KEY",
		LoginKind: LoginAPIKey,
	},

	// -----------------------------------------------------------------------
	// Local providers (no interactive login; env/config only)
	// -----------------------------------------------------------------------
	"ollama": {
		ID:        "ollama",
		Name:      "Ollama (Local)",
		Protocol:  ProtocolOllamaChat,
		BaseURL:   "http://127.0.0.1:11434",
		EnvVar:    "",
		LoginKind: LoginNone,
	},
	"lm-studio": {
		ID:        "lm-studio",
		Name:      "LM Studio (Local)",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "http://127.0.0.1:1234/v1",
		EnvVar:    "",
		LoginKind: LoginNone,
	},
	"vllm": {
		ID:        "vllm",
		Name:      "vLLM (Local)",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "http://127.0.0.1:8000/v1",
		EnvVar:    "",
		LoginKind: LoginNone,
	},
	"litellm": {
		ID:        "litellm",
		Name:      "LiteLLM",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "http://127.0.0.1:4000/v1",
		EnvVar:    "LITELLM_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"llama.cpp": {
		ID:        "llama.cpp",
		Name:      "llama.cpp (Local)",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "http://127.0.0.1:8080/v1",
		EnvVar:    "LLAMA_CPP_API_KEY",
		LoginKind: LoginNone,
	},

	// -----------------------------------------------------------------------
	// Google Gemini (API key, env-only)
	// -----------------------------------------------------------------------
	"google": {
		ID:        "google",
		Name:      "Google Gemini (API Key)",
		Protocol:  ProtocolGoogleGemini,
		BaseURL:   "https://generativelanguage.googleapis.com/v1beta",
		EnvVar:    "GEMINI_API_KEY",
		LoginKind: LoginNone,
	},
	"google-vertex": {
		ID:        "google-vertex",
		Name:      "Google Vertex AI",
		Protocol:  ProtocolGoogleVertex,
		BaseURL:   "https://aiplatform.googleapis.com",
		EnvVar:    "GOOGLE_CLOUD_API_KEY",
		LoginKind: LoginNone,
	},

	// -----------------------------------------------------------------------
	// Amazon Bedrock (env-only, AWS credentials from env/profile)
	// -----------------------------------------------------------------------
	"amazon-bedrock": {
		ID:        "amazon-bedrock",
		Name:      "Amazon Bedrock",
		Protocol:  ProtocolBedrockConverse,
		BaseURL:   "", // derived from region at runtime
		EnvVar:    "AWS_BEARER_TOKEN_BEDROCK",
		LoginKind: LoginNone,
	},

	// -----------------------------------------------------------------------
	// Azure OpenAI Responses (API key, env-only)
	// -----------------------------------------------------------------------
	"azure": {
		ID:        "azure",
		Name:      "Azure OpenAI",
		Protocol:  ProtocolAzureResponses,
		BaseURL:   "", // resolved from AZURE_OPENAI_BASE_URL or AZURE_OPENAI_RESOURCE_NAME
		EnvVar:    "AZURE_OPENAI_API_KEY",
		LoginKind: LoginAPIKey,
	},

	// -----------------------------------------------------------------------
	// Xiaomi token-plan providers (API key, regional)
	// -----------------------------------------------------------------------
	"xiaomi-token-plan-sgp": {
		ID:        "xiaomi-token-plan-sgp",
		Name:      "Xiaomi Token Plan (Singapore)",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://token-plan-sgp.xiaomimimo.com/v1",
		EnvVar:    "XIAOMI_TOKEN_PLAN_SGP_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"xiaomi-token-plan-ams": {
		ID:        "xiaomi-token-plan-ams",
		Name:      "Xiaomi Token Plan (Europe)",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://token-plan-ams.xiaomimimo.com/v1",
		EnvVar:    "XIAOMI_TOKEN_PLAN_AMS_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"xiaomi-token-plan-cn": {
		ID:        "xiaomi-token-plan-cn",
		Name:      "Xiaomi Token Plan (China)",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://token-plan-cn.xiaomimimo.com/v1",
		EnvVar:    "XIAOMI_TOKEN_PLAN_CN_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"xiaomi": {
		ID:        "xiaomi",
		Name:      "Xiaomi MiMo",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://api.xiaomimimo.com/v1",
		EnvVar:    "XIAOMI_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"alibaba-coding-plan": {
		ID:        "alibaba-coding-plan",
		Name:      "Alibaba Cloud (DashScope Coding)",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://coding-intl.dashscope.aliyuncs.com/v1",
		EnvVar:    "ALIBABA_CODING_PLAN_API_KEY",
		LoginKind: LoginAPIKey,
	},
	"alibaba-token-plan": {
		ID:        "alibaba-token-plan",
		Name:      "Alibaba Cloud (Bailian Token Plan)",
		Protocol:  ProtocolOpenAICompletions,
		BaseURL:   "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1",
		EnvVar:    "ALIBABA_TOKEN_PLAN_API_KEY",
		LoginKind: LoginAPIKey,
	},
}

// Get returns the Definition for provider id, or ok=false if unknown.
func Get(id string) (Definition, bool) {
	d, ok := registry[id]
	return d, ok
}

// All returns every registered provider definition, in registration order.
// Used by /login and `nib login --list` to build the provider picker.
func All() []Definition {
	out := make([]Definition, 0, len(providerOrder))
	for _, id := range providerOrder {
		if d, ok := registry[id]; ok {
			out = append(out, d)
		}
	}
	return out
}

// Loginable returns only providers with an interactive /login flow
// (LoginKind != LoginNone). These are what /login's picker lists.
func Loginable() []Definition {
	var out []Definition
	for _, d := range All() {
		if d.LoginKind != LoginNone {
			out = append(out, d)
		}
	}
	return out
}
