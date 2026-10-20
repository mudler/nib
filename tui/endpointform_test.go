package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/types"
	"gopkg.in/yaml.v3"
)

func TestEveryEndpointTemplatePreserved(t *testing.T) {
	sawAnthropic := false
	for i, tmpl := range endpointTemplates {
		t.Run(tmpl.name, func(t *testing.T) {
			if tmpl.provider == "anthropic-api-key" {
				sawAnthropic = true
				if tmpl.baseURL != "https://api.anthropic.com" {
					t.Fatal("wrong official URL")
				}
			}
			m := newEndpointTestModel(t)
			m.openEndpointForm()
			for j := 0; j < i; j++ {
				m, _ = press(t, m, tea.KeyMsg{Type: tea.KeyDown})
			}
			m, _ = press(t, m, keyEnter)
			name := fmt.Sprintf("template-%d", i)
			m, _ = press(t, m, typeText(name), tea.KeyMsg{Type: tea.KeyTab})
			url := tmpl.baseURL
			if url == "" {
				url = "http://custom.invalid/v1"
				m, _ = press(t, m, typeText(url))
			}
			m, _ = press(t, m, tea.KeyMsg{Type: tea.KeyTab}, typeText("model-sentinel"), tea.KeyMsg{Type: tea.KeyTab}, typeText("endpoint-private-sentinel"))
			d := m.endpointForm.formDialog()
			if !strings.Contains(d.Title, tmpl.name) {
				t.Fatalf("title %q, template %q", d.Title, tmpl.name)
			}
			if strings.Contains(fmt.Sprint(d), "endpoint-private-sentinel") || !strings.Contains(fmt.Sprint(d), "•") {
				t.Fatal("unmasked key")
			}
			m, _ = press(t, m, keyEnter)
			if m.endpointForm.active {
				t.Fatalf("submit failed: %s", m.endpointForm.err)
			}
			data, err := os.ReadFile(filepath.Join(m.cfg.BaseDir, "config.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var cfg types.Config
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				t.Fatal(err)
			}
			if len(cfg.Endpoints) != 1 {
				t.Fatalf("endpoints: %+v", cfg.Endpoints)
			}
			got := cfg.Endpoints[0].ModelProviderConfig
			if got.Provider != tmpl.provider || got.BaseURL != url || got.Model != "model-sentinel" || got.APIKey != "endpoint-private-sentinel" {
				t.Fatalf("persisted: %+v", got)
			}
		})
	}
	if !sawAnthropic {
		t.Fatal("Anthropic API-key template absent")
	}
}
