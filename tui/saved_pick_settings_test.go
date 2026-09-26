package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/config"
	"github.com/mudler/nib/types"
)

// fileBackedSession builds a session the way app does for a config.yaml
// user: loaded from a file under a temp root, registered as its source the way app registers it.
// The endpoint lists model-a and model-b.
func fileBackedSession(t *testing.T) (Model, func() types.Config, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []map[string]string{
			{"id": "model-a", "object": "model"}, {"id": "model-b", "object": "model"},
		}})
	}))
	t.Cleanup(srv.Close)

	base := t.TempDir()
	path := config.WritablePathIn(base)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	yaml := "model: model-a\nbase_url: " + srv.URL + "/v1\ncompaction:\n  max_context_tokens: 128000\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := config.LoadOptions{BaseDir: base, SkipBareEnv: true}
	config.RegisterStartupSource(opts) // as app does
	load := func() types.Config { return config.LoadWith(opts) }
	cfg := load()
	s, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	m := newQueueTestModel()
	m.ctx = context.Background()
	m.cfg = cfg
	m.session = s
	return m, load, path
}

func ctrlSPickModelB(t *testing.T, m Model) Model {
	t.Helper()
	m.modelPicker.open(1)
	m.modelPicker.setModels([]string{"model-a", "model-b"}, "model-a")
	m.modelPicker.loading = false
	m.modelPicker.move(1)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = next.(Model)
	if m.session.Model() != "model-b" {
		t.Fatalf("ctrl+s left the session on %q, want model-b", m.session.Model())
	}
	return m
}

func restartOn(t *testing.T, cfg types.Config) *chat.Session {
	t.Helper()
	s, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// /settings changes config.yaml mid-session, then ctrl+s saves a model: the
// save is the last change, so the next start keeps it, with no note.
func TestCtrlSAfterASettingsEditSticksOnRestart(t *testing.T) {
	m, load, _ := fileBackedSession(t)
	s, err := config.LookupSetting("model")
	if err != nil {
		t.Fatal(err)
	}
	m.setSetting(s, "model-c")
	if got := load().Model; got != "model-c" {
		t.Fatalf("config.yaml model = %q after /settings, want model-c", got)
	}
	m = ctrlSPickModelB(t, m)

	next := restartOn(t, load())
	if next.Model() != "model-b" {
		t.Fatalf("restart Model = %q, want the model saved after the /settings edit", next.Model())
	}
	if note := next.StartupNote(); note != "" {
		t.Fatalf("StartupNote = %q, want none", note)
	}
}

// The same for config.yaml edited by another program while the session runs.
func TestCtrlSAfterAnExternalConfigEditSticksOnRestart(t *testing.T) {
	m, load, path := fileBackedSession(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte("provider: openai\napi_key_env: EDITED_KEY\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	m = ctrlSPickModelB(t, m)

	next := restartOn(t, load())
	if next.Model() != "model-b" {
		t.Fatalf("restart Model = %q, want the model saved after the external edit", next.Model())
	}
	if note := next.StartupNote(); note != "" {
		t.Fatalf("StartupNote = %q, want none", note)
	}

	// An edit after the save still wins, with the note.
	if err := config.WriteSetting(path, "model", "model-d"); err != nil {
		t.Fatal(err)
	}
	after := restartOn(t, load())
	if after.Model() != "model-d" || after.StartupNote() == "" {
		t.Fatalf("after a later edit: Model = %q note = %q, want model-d with a note", after.Model(), after.StartupNote())
	}
}
