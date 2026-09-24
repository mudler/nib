package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/provider"
	"github.com/mudler/nib/types"
	"gopkg.in/yaml.v3"
)

// Selecting the Ollama preset (index 3) should move to the fields step and
// prefill the base URL and model from that preset.
func TestProviderSelectionPrefillsFields(t *testing.T) {
	m := newModel(context.Background(), types.Config{})

	// Move cursor down 3 times: Anthropic(0) -> OpenAI(1) -> Local(2) -> Ollama(3).
	var mi tea.Model
	for i := 0; i < 3; i++ {
		mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = mi.(model)
	}

	if m.cursor != 3 {
		t.Fatalf("cursor = %d, want 3", m.cursor)
	}

	// Enter selects the preset and advances to the fields step.
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mi.(model)

	if m.step != stepFields {
		t.Fatalf("step = %d, want stepFields(%d)", m.step, stepFields)
	}
	if got := m.inputs[fieldBaseURL].Value(); got != "http://localhost:11434/v1" {
		t.Errorf("base url = %q", got)
	}
	if got := m.inputs[fieldModel].Value(); got != "llama3.1" {
		t.Errorf("model = %q", got)
	}
}

func oauthModel(t *testing.T) model {
	t.Helper()
	m := newModel(context.Background(), types.Config{BaseDir: t.TempDir(), APIKey: "old-key"})
	for i, p := range m.presets {
		if p.Provider == "openai-codex" {
			m.cursor = i
			mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			return mi.(model)
		}
	}
	t.Fatal("OAuth preset missing")
	return m
}

func TestOAuthSetupSavesProviderAndSeparateCredentials(t *testing.T) {
	m := oauthModel(t)
	if m.focus != fieldModel || strings.Contains(m.View(), "API key") || strings.Contains(m.View(), "Base URL") {
		t.Fatal("OAuth setup should show and focus only the model field")
	}
	m.startLogin = func(ctx context.Context, store *auth.Store, def provider.Definition) (*auth.LoginFlow, error) {
		if def.ID != "openai-codex" || store.Path != filepath.Join(m.cfg.BaseDir, "credentials.json") {
			t.Fatalf("wrong login provider or credential root: %s %s", def.ID, store.Path)
		}
		return auth.NewLoginFlow(def.ID, "test authorization instructions", "", func(context.Context) (auth.Credential, error) {
			cred := auth.Credential{ProviderID: def.ID, Kind: auth.CredentialOAuth, AccessToken: "test-token"}
			return cred, store.Save(cred)
		}), nil
	}
	mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mi.(model)
	if !m.probing || !strings.Contains(m.View(), "test authorization instructions") || cmd == nil {
		t.Fatal("login should display instructions while waiting")
	}
	mi, _ = m.Update(cmd())
	m = mi.(model)
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mi.(model)
	if !m.saved || m.cfg.Provider != "openai-codex" || m.cfg.APIKey != "" || m.cfg.BaseURL != "" {
		t.Fatalf("wrong saved OAuth config: provider=%s saved=%v", m.cfg.Provider, m.saved)
	}
	data, err := os.ReadFile(m.savedPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg types.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "openai-codex" || cfg.Model == "" || strings.Contains(string(data), "test-token") {
		t.Fatal("provider/model must persist without OAuth tokens in config.yaml")
	}
	if _, ok, err := auth.NewStore(filepath.Join(m.cfg.BaseDir, "credentials.json")).Get("openai-codex"); err != nil || !ok {
		t.Fatalf("credentials not saved: %v", err)
	}
}

func TestOAuthSetupFailuresCannotSave(t *testing.T) {
	for _, startFailure := range []bool{true, false} {
		m := oauthModel(t)
		m.startLogin = func(context.Context, *auth.Store, provider.Definition) (*auth.LoginFlow, error) {
			if startFailure {
				return nil, errors.New("callback port busy")
			}
			return auth.NewLoginFlow("openai-codex", "instructions", "", func(context.Context) (auth.Credential, error) {
				return auth.Credential{}, errors.New("authorization denied")
			}), nil
		}
		mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = mi.(model)
		if cmd != nil {
			mi, _ = m.Update(cmd())
			m = mi.(model)
		}
		if m.probing || m.probeErr == nil {
			t.Fatal("expected visible login failure")
		}
		mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = mi.(model)
		if m.saved || m.step != stepProbe {
			t.Fatal("failed login must not save")
		}
		mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
		if mi.(model).step != stepFields {
			t.Fatal("failed login must allow retry")
		}
	}
}

func TestOAuthSetupCancelStopsLogin(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyEsc, tea.KeyCtrlC} {
		m := oauthModel(t)
		var loginCtx context.Context
		m.startLogin = func(ctx context.Context, _ *auth.Store, _ provider.Definition) (*auth.LoginFlow, error) {
			loginCtx = ctx
			return auth.NewLoginFlow("openai-codex", "instructions", "", func(ctx context.Context) (auth.Credential, error) {
				return auth.Credential{}, ctx.Err()
			}), nil
		}
		mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = mi.(model)
		mi, _ = m.Update(tea.KeyMsg{Type: key})
		if !mi.(model).quitting || loginCtx.Err() == nil {
			t.Fatal("cancel must stop pending OAuth")
		}
		cmd()
	}
}

// Esc on the provider step cancels (no save).
func TestProviderEscCancels(t *testing.T) {
	m := newModel(context.Background(), types.Config{})
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mi.(model)
	if !m.quitting || m.saved {
		t.Fatalf("esc should cancel without saving: quitting=%v saved=%v", m.quitting, m.saved)
	}
}

func TestKeyRequiredTracksPreset(t *testing.T) {
	// OpenAI (index 1) requires a key.
	m := newModel(context.Background(), types.Config{})
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown}) // Anthropic(0) -> OpenAI(1)
	m = mi.(model)
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mi.(model)
	if !m.keyRequired {
		t.Errorf("OpenAI preset should set keyRequired=true")
	}

	// Ollama (index 3) does not.
	m2 := newModel(context.Background(), types.Config{})
	for i := 0; i < 3; i++ {
		mi2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyDown})
		m2 = mi2.(model)
	}
	mi2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m2 = mi2.(model)
	if m2.keyRequired {
		t.Errorf("Ollama preset should leave keyRequired=false")
	}
}
