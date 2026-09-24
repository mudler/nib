package setup

// Preset is a provider template that prefills connection fields in the wizard.
type Preset struct {
	Provider     string // transport ID; empty uses the OpenAI-compatible transport
	Name         string // display name shown in the picker
	BaseURL      string // prefilled, editable; "" means the OpenAI SDK default
	DefaultModel string // prefilled model field
	DefaultKey   string // prefilled api_key
	KeyRequired  bool   // informational hint only; never blocks saving
}

// Presets returns the built-in provider presets, in display order.
func Presets() []Preset {
	return []Preset{
		{Name: "Anthropic", BaseURL: "https://api.anthropic.com", DefaultModel: "claude-sonnet-4-5-20250929", DefaultKey: "", KeyRequired: false},
		{Name: "OpenAI", BaseURL: "", DefaultModel: "gpt-4o-mini", DefaultKey: "", KeyRequired: true},
		{Name: "ChatGPT / OpenAI OAuth", Provider: "openai-codex", DefaultModel: "gpt-5-codex"},
		{Name: "Local (LocalAI / llama.cpp / vLLM)", BaseURL: "http://localhost:8080/v1", DefaultModel: "", DefaultKey: "sk-no-key", KeyRequired: false},
		{Name: "Ollama", BaseURL: "http://localhost:11434/v1", DefaultModel: "llama3.1", DefaultKey: "sk-no-key", KeyRequired: false},
		{Name: "Google Gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta", DefaultModel: "gemini-2.5-flash", DefaultKey: "", KeyRequired: true},
		{Name: "Groq", BaseURL: "https://api.groq.com/openai/v1", DefaultModel: "llama-3.3-70b-versatile", DefaultKey: "", KeyRequired: true},
		{Name: "DeepSeek", BaseURL: "https://api.deepseek.com", DefaultModel: "deepseek-chat", DefaultKey: "", KeyRequired: true},
		{Name: "Together AI", BaseURL: "https://api.together.xyz/v1", DefaultModel: "meta-llama/Llama-3.3-70B-Instruct-Turbo", DefaultKey: "", KeyRequired: true},
		{Name: "xAI (Grok)", BaseURL: "https://api.x.ai/v1", DefaultModel: "grok-3-mini", DefaultKey: "", KeyRequired: true},
		{Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1", DefaultModel: "anthropic/claude-3.5-sonnet", DefaultKey: "", KeyRequired: true},
		{Name: "Mistral", BaseURL: "https://api.mistral.ai/v1", DefaultModel: "mistral-large-latest", DefaultKey: "", KeyRequired: true},
		{Name: "Custom", BaseURL: "", DefaultModel: "", DefaultKey: "", KeyRequired: false},
	}
}
