package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/endpoint"
	"github.com/mudler/nib/plugin"
	"github.com/mudler/nib/theme"
	"github.com/mudler/nib/types"
)

// newRegoloOverrideModel reproduces the reported setup: config.yaml names one
// model ("uncensored") while a /login pick (Regolo, glm5.2) is what the
// session really talks to. BaseDir is a temp dir so neither the user's real
// provider.json nor credentials.json leaks in. No request is ever sent.
func newRegoloOverrideModel(t *testing.T) Model {
	t.Helper()
	cfg := types.Config{
		Model:      "uncensored",
		BaseURL:    "http://127.0.0.1:1/v1",
		BaseDir:    t.TempDir(),
		Compaction: types.CompactionConfig{MaxContextTokens: 128000},
	}
	s, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.SaveAPIKey("regolo", "rg-secret", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SwitchProvider("regolo", "glm5.2"); err != nil {
		t.Fatal(err)
	}

	m := newQueueTestModel()
	m.ctx = context.Background()
	m.cfg = cfg
	m.session = s
	return m
}

func bootLine(t *testing.T, b *bootState, ev string) string {
	t.Helper()
	for _, e := range b.entries {
		if e.ev == ev {
			return e.dt
		}
	}
	t.Fatalf("boot log has no %q line: %+v", ev, b.entries)
	return ""
}

// The boot log's provider and model lines name what requests really use, and
// say so when a saved pick overrides config.yaml's model.
func TestBootLogNamesTheActiveProviderAndModel(t *testing.T) {
	m := newRegoloOverrideModel(t)
	m.boot = newBootState()
	for range bootScript() {
		m.boot.tick(&m)
	}

	if got := bootLine(t, m.boot, "provider"); got != "Regolo" {
		t.Fatalf("provider line = %q, want Regolo", got)
	}
	model := bootLine(t, m.boot, "model")
	if !strings.HasPrefix(model, "glm5.2") {
		t.Fatalf("model line = %q, want it to start with the active model glm5.2", model)
	}
	if !strings.Contains(model, "config.yaml: uncensored") || !strings.Contains(model, "overridden by a saved pick") {
		t.Fatalf("model line = %q, want a note that a saved pick overrides config.yaml's uncensored", model)
	}
}

// Without an override (config.yaml's own endpoint), the model line carries no
// note: there is nothing to reconcile.
func TestBootLogOmitsTheNoteWithoutAnOverride(t *testing.T) {
	m := newModelSwitchTestModel(t, "model-a")
	m.boot = newBootState()
	for range bootScript() {
		m.boot.tick(&m)
	}
	if got := bootLine(t, m.boot, "model"); got != "model-a" {
		t.Fatalf("model line = %q, want just model-a", got)
	}
	if got := bootLine(t, m.boot, "provider"); got != chat.ConfigProviderName {
		t.Fatalf("provider line = %q, want %q", got, chat.ConfigProviderName)
	}
}

// The session is built asynchronously, so the provider/model lines can be
// printed from config.yaml before it exists. sessionReadyMsg must correct them
// rather than leave config.yaml's model on screen.
func TestBootLogIsCorrectedWhenTheSessionArrives(t *testing.T) {
	m := newRegoloOverrideModel(t)
	s := m.session
	m.session = nil
	m.sessionReady = false
	m.boot = newBootState()
	for range 4 { // core/init, config, provider, model
		m.boot.tick(&m)
	}
	if got := bootLine(t, m.boot, "model"); got != "uncensored" {
		t.Fatalf("pre-session model line = %q, want the config.yaml fallback", got)
	}

	next, _ := m.Update(sessionReadyMsg{session: s})
	m = next.(Model)
	if got := bootLine(t, m.boot, "provider"); got != "Regolo" {
		t.Fatalf("provider line after session = %q, want Regolo", got)
	}
	if got := bootLine(t, m.boot, "model"); !strings.HasPrefix(got, "glm5.2") {
		t.Fatalf("model line after session = %q, want glm5.2", got)
	}
}

// A session that is ready before the provider/model ticks fire flushes them
// through markReady, which must fill in real details too, not blanks.
func TestBootLogFlushFillsProviderAndModel(t *testing.T) {
	m := newRegoloOverrideModel(t)
	m.boot = newBootState()
	m.boot.markReady(&m)
	if got := bootLine(t, m.boot, "provider"); got != "Regolo" {
		t.Fatalf("flushed provider line = %q, want Regolo", got)
	}
	if got := bootLine(t, m.boot, "model"); !strings.HasPrefix(got, "glm5.2") {
		t.Fatalf("flushed model line = %q, want glm5.2", got)
	}
}

func TestHeaderStatsCarryTheActiveProvider(t *testing.T) {
	m := newRegoloOverrideModel(t)
	hs := m.viewState().HeaderStats
	if hs.Provider != "regolo" || hs.Model != "glm5.2" {
		t.Fatalf("header stats = %+v, want provider regolo and model glm5.2", hs)
	}

	cfgModel := newModelSwitchTestModel(t, "model-a")
	if hs := cfgModel.viewState().HeaderStats; hs.Provider != chat.ConfigProviderName {
		t.Fatalf("config.yaml header provider = %q, want %q", hs.Provider, chat.ConfigProviderName)
	}
}

// /model lists the current provider's models only, so its picker names that
// provider and points at /login for switching provider.
func TestModelPickerNamesTheCurrentProvider(t *testing.T) {
	m := newRegoloOverrideModel(t)
	m.modelPicker.open(1)
	m.modelPicker.setModels([]string{"glm5.2", "qwen"}, "glm5.2")
	d := m.buildModelPickerDialog()
	if !strings.HasPrefix(d.Title, "Regolo ") {
		t.Fatalf("title = %q, want it to name Regolo", d.Title)
	}
	if !strings.Contains(d.Hint, theme.ModelPickerLoginHint) {
		t.Fatalf("hint = %q, want the /login hint", d.Hint)
	}
}

// ctrl+s in the /model picker switches and saves the pick as the default,
// and the hint names both keys.
func TestModelPickerCtrlSSavesTheDefault(t *testing.T) {
	m := newModelSwitchTestModel(t, "model-a", "model-b")
	m.modelPicker.open(1)
	m.modelPicker.setModels([]string{"model-a", "model-b"}, "model-a")
	m.modelPicker.loading = false
	if d := m.buildModelPickerDialog(); !strings.Contains(d.Hint, "ctrl+s") || !strings.Contains(d.Hint, "enter use in this session") {
		t.Fatalf("hint = %q, want both the session-only and the save-as-default keys", d.Hint)
	}
	m.modelPicker.move(1)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = next.(Model)
	if m.modelPicker.active || m.session.Model() != "model-b" {
		t.Fatalf("ctrl+s picker active=%v model=%q, want closed on model-b", m.modelPicker.active, m.session.Model())
	}
	want := "model: model-b · " + theme.ProviderSavedDefault
	if msg := lastMessage(t, m); msg.Content != want {
		t.Fatalf("confirmation = %q, want %q", msg.Content, want)
	}
	next2, err := chat.NewSession(context.Background(), m.cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { next2.Close() })
	if got := next2.Model(); got != "model-b" {
		t.Fatalf("next session Model = %q, want the saved model-b", got)
	}
}

// A /model pick applies to this session only, on the default endpoint and
// on a /login provider alike, and the confirmation says so rather than
// promising a saved default that other sessions would inherit.
func TestModelPickerSaysThePickIsForThisSessionOnly(t *testing.T) {
	for name, m := range map[string]Model{
		"default endpoint": newModelSwitchTestModel(t, "model-a", "model-b"),
		"login provider":   newRegoloOverrideModel(t),
	} {
		m.modelPicker.open(1)
		m.modelPicker.setModels([]string{"model-a", "model-b"}, "model-a")
		m.modelPicker.move(1)

		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(Model)
		msg := lastMessage(t, m)
		want := "model: model-b · " + theme.ModelSessionOnly
		if msg.Content != want {
			t.Fatalf("%s: confirmation = %q, want %q", name, msg.Content, want)
		}
	}
}

// writeSavedPick seeds provider.json under base's plugin state directory, the
// same file endpoint.WriteSaved/LoadSaved read, so a session built with
// BaseDir: base starts on sv as if a previous run had picked it.
func writeSavedPick(t *testing.T, base string, sv endpoint.Saved) {
	t.Helper()
	path := filepath.Join(plugin.BaseDirIn(base), chat.ProviderStateFile)
	if err := endpoint.WriteSaved(path, sv); err != nil {
		t.Fatalf("writeSavedPick: %v", err)
	}
}

// The header names a named config.yaml endpoint by its "@"-prefixed ID, the
// same ID EndpointID() and the /endpoint picker use, not the generic
// chat.ConfigProviderName it shows for the default endpoint.
func TestHeaderNamesTheActiveNamedEndpoint(t *testing.T) {
	m := newEndpointTestModel(t)
	if cmd := m.dispatchResolved("/endpoint @home"); cmd != nil {
		t.Fatal("/endpoint <id> must not start a turn")
	}
	if got := m.headerProvider(); got != "@home" {
		t.Fatalf("headerProvider() = %q, want @home", got)
	}
}

// A saved pick that no longer resolves (its named endpoint is gone from
// config.yaml) must not vanish silently: Session.StartupNote() names it, and
// the boot log must actually say so, not just carry the accessor unused.
func TestBootLogReportsADroppedSavedPick(t *testing.T) {
	base := t.TempDir()
	writeSavedPick(t, base, endpoint.Saved{ID: "@gone", Model: "m"})
	cfg := types.Config{
		Model:      "default-model",
		BaseURL:    "http://127.0.0.1:1/v1",
		BaseDir:    base,
		Compaction: types.CompactionConfig{MaxContextTokens: 128000},
	}
	s, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	if !strings.Contains(s.StartupNote(), "@gone") {
		t.Fatalf("StartupNote = %q, want it to name @gone", s.StartupNote())
	}

	m := newQueueTestModel()
	m.ctx = context.Background()
	m.cfg = cfg
	m.session = s
	m.boot = newBootState()
	m.boot.markReady(&m)

	view := m.View()
	if !strings.Contains(view, "@gone") {
		t.Fatalf("boot log does not mention the dropped pick:\n%s", view)
	}
}

// A config.yaml endpoint rejected at load (here: no base_url or provider, so
// it cannot address anywhere) must name itself in the boot log instead of
// vanishing — Session.ConfigErrors() already exists to carry exactly this,
// per the comment on types.Endpoints.Validate.
func TestBootLogReportsARejectedEndpoint(t *testing.T) {
	cfg := types.Config{
		Model:      "default-model",
		BaseURL:    "http://127.0.0.1:1/v1",
		BaseDir:    t.TempDir(),
		Compaction: types.CompactionConfig{MaxContextTokens: 128000},
		Endpoints: types.Endpoints{{
			Name:                "bad",
			ModelProviderConfig: types.ModelProviderConfig{Model: "m"},
		}},
	}
	s, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if len(s.ConfigErrors()) == 0 {
		t.Fatal("test setup: the bad endpoint should have been rejected")
	}

	m := newQueueTestModel()
	m.ctx = context.Background()
	m.cfg = cfg
	m.session = s
	m.boot = newBootState()
	m.boot.markReady(&m)

	view := m.View()
	if !strings.Contains(view, "bad") {
		t.Fatalf("boot log does not name the rejected endpoint:\n%s", view)
	}
}

// The carried requirement: bootModel's override note must fire on actual
// model divergence, not on endpoint identity. A model pick saved on
// config.yaml's OWN default endpoint (SetModel now persists uniformly on
// every endpoint, including the default) shadows config.yaml's model just as
// much as a /login pick does, while EndpointID() never leaves "config" — the
// old identity-only gate hid this case entirely.
func TestBootLogNotesADefaultEndpointModelOverride(t *testing.T) {
	base := t.TempDir()
	cfg := types.Config{
		Model:      "default-model",
		BaseURL:    "http://127.0.0.1:1/v1",
		BaseDir:    base,
		Compaction: types.CompactionConfig{MaxContextTokens: 128000},
	}
	first, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := first.SetModel("saved-model"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if err := first.SaveModelAsDefault(); err != nil {
		t.Fatalf("SaveModelAsDefault: %v", err)
	}
	first.Close()

	s, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession (restart): %v", err)
	}
	t.Cleanup(func() { s.Close() })

	if got := s.EndpointID(); got != chat.ConfigProviderID {
		t.Fatalf("EndpointID = %q, want the restarted session to still be on the default endpoint", got)
	}
	if s.Model() == s.ConfigModel() {
		t.Fatalf("test setup: Model() and ConfigModel() should diverge, both are %q", s.Model())
	}

	m := newQueueTestModel()
	m.ctx = context.Background()
	m.cfg = cfg
	m.session = s

	got := m.bootModel()
	if !strings.Contains(got, "config.yaml: default-model") || !strings.Contains(got, "overridden by a saved pick") {
		t.Fatalf("bootModel() = %q, want the same honest note the /login case gets, naming config.yaml's default-model", got)
	}
	if !strings.HasPrefix(got, "saved-model") {
		t.Fatalf("bootModel() = %q, want it to lead with the running model saved-model", got)
	}
}

// A named endpoint's own model: key, not the unrelated top-level one, is
// what the boot note must cite. ConfigModel() is hard-wired to the top-level
// block (default-model here); @work's own declared model is work-model, and
// that is the value a saved pick on @work overrides.
func TestBootLogNotesANamedEndpointModelOverride(t *testing.T) {
	base := t.TempDir()
	cfg := types.Config{
		Model:      "default-model",
		BaseURL:    "http://127.0.0.1:1/v1",
		BaseDir:    base,
		Compaction: types.CompactionConfig{MaxContextTokens: 128000},
		Endpoints: types.Endpoints{{
			Name:                "work",
			ModelProviderConfig: types.ModelProviderConfig{BaseURL: "https://vllm.corp/v1", Model: "work-model"},
		}},
	}
	first, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := first.SwitchProvider("@work", "work-model-pinned"); err != nil {
		t.Fatalf("SwitchProvider: %v", err)
	}
	first.Close()

	s, err := chat.NewSession(context.Background(), cfg, chat.Callbacks{})
	if err != nil {
		t.Fatalf("NewSession (restart): %v", err)
	}
	t.Cleanup(func() { s.Close() })

	if got := s.EndpointID(); got != "@work" {
		t.Fatalf("EndpointID = %q, want the restarted session on @work", got)
	}
	if s.ConfigModel() != "default-model" {
		t.Fatalf("test setup: ConfigModel() = %q, want the unrelated top-level model default-model", s.ConfigModel())
	}

	m := newQueueTestModel()
	m.ctx = context.Background()
	m.cfg = cfg
	m.session = s

	got := m.bootModel()
	if !strings.Contains(got, "config.yaml: work-model") || !strings.Contains(got, "overridden by a saved pick") {
		t.Fatalf("bootModel() = %q, want a note naming @work's own model (work-model), not the top-level one", got)
	}
	if strings.Contains(got, "default-model") {
		t.Fatalf("bootModel() = %q, must not cite the unrelated top-level model", got)
	}
	if !strings.HasPrefix(got, "work-model-pinned") {
		t.Fatalf("bootModel() = %q, want it to lead with the running model work-model-pinned", got)
	}
}

func TestModelsListingStartsWithTheProvider(t *testing.T) {
	m := newModelSwitchTestModel(t, "model-a", "model-b")
	if cmd := m.dispatchResolved("/models"); cmd != nil {
		t.Fatal("/models must not start a turn")
	}
	msg := lastMessage(t, m)
	if !strings.Contains(msg.Content, chat.ConfigProviderName+" models:") {
		t.Fatalf("listing = %q, want it headed by the provider", msg.Content)
	}
}
