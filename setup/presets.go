package setup

// Preset is a provider template that prefills connection fields in the wizard.
type Preset struct {
	Name         string // display name shown in the picker
	BaseURL      string // prefilled, editable; "" means the OpenAI SDK default
	DefaultModel string // prefilled model field
	DefaultKey   string // prefilled api_key
	KeyRequired  bool   // informational hint only; never blocks saving
}

// Presets returns the built-in provider presets, in display order.
func Presets() []Preset {
	return []Preset{
		{Name: "OpenAI", BaseURL: "", DefaultModel: "gpt-4o-mini", DefaultKey: "", KeyRequired: true},
		{Name: "Local (LocalAI / llama.cpp / vLLM)", BaseURL: "http://localhost:8080/v1", DefaultModel: "", DefaultKey: "sk-no-key", KeyRequired: false},
		{Name: "Ollama", BaseURL: "http://localhost:11434/v1", DefaultModel: "llama3.1", DefaultKey: "sk-no-key", KeyRequired: false},
		{Name: "Custom", BaseURL: "", DefaultModel: "", DefaultKey: "", KeyRequired: false},
	}
}
