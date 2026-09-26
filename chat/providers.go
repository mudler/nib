package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/mudler/xlog"

	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/config"
	"github.com/mudler/nib/endpoint"
	"github.com/mudler/nib/provider"
	"github.com/mudler/nib/types"
)

// ConfigProviderID is endpoint.DefaultID under its pre-endpoints name, kept
// so existing call sites and embedders keep compiling.
const ConfigProviderID = endpoint.DefaultID

// ConfigProviderName is how the UI names the default endpoint.
const ConfigProviderName = endpoint.DefaultName

// ProviderEntry is one row of the provider picker: a registry provider, a
// config.yaml named endpoint, or the config.yaml default endpoint, with its
// login state.
type ProviderEntry struct {
	ID        string
	Name      string
	Kind      endpoint.Kind
	LoginKind provider.LoginKind
	// Ready means requests can authenticate right now: a stored login, the
	// provider's environment variable, or no credential needed at all.
	Ready bool
	// Stored means a /login credential exists (so /logout can remove it).
	Stored bool
	// Status is a short human description of Ready/Stored.
	Status string
	// Current marks the endpoint the session is talking to.
	Current bool
	// NeedsBaseURL means logging in must also collect an endpoint.
	NeedsBaseURL bool
	// EnvVar is the environment variable that can hold the key instead.
	EnvVar string
	// Model is what this entry would run, empty when it must be picked.
	Model string
}

// EndpointID returns the picker ID of the endpoint the session is currently
// talking to: ConfigProviderID until a named endpoint or /login provider is
// selected.
func (s *Session) EndpointID() string {
	s.modelMu.RLock()
	defer s.modelMu.RUnlock()
	return s.endpointID
}

// ProviderID is EndpointID under its pre-endpoints name.
func (s *Session) ProviderID() string { return s.EndpointID() }

// StartupNote is the non-empty explanation when a saved pick could not be
// honored at startup, for the boot log. Empty otherwise.
func (s *Session) StartupNote() string { return s.startupNote }

// ConfigErrors are the config.yaml endpoints that were rejected at load.
func (s *Session) ConfigErrors() []error { return s.configErrs }

// DetectedLSPServers returns human-readable lines describing the language
// servers configured on the session (explicit config plus auto-detected). It
// is nil when no servers are configured. Used by the boot screen.
func (s *Session) DetectedLSPServers() []string {
	if s.lspManager == nil {
		return nil
	}
	return s.lspManager.ConfigLines()
}

// ActiveProviderName is the display name of the endpoint the session is
// talking to right now: the registry name of a /login provider ("Regolo"),
// a config.yaml named endpoint ("@work"), or ConfigProviderName while
// config.yaml's default endpoint is in use.
//
// It is the single source of truth for "which provider" in the UI. A saved
// pick overrides config.yaml at startup (restoreStartupEndpoint), so a front
// end that read types.Config.Provider/Model instead would name an endpoint
// and model that no request goes to.
func (s *Session) ActiveProviderName() string {
	id := s.EndpointID()
	if id == "" || id == ConfigProviderID {
		return ConfigProviderName
	}
	if e, ok := s.endpoints.Lookup(id); ok {
		return e.Name
	}
	if def, ok := provider.Get(id); ok && def.Name != "" {
		return def.Name
	}
	return id
}

// ConfigModel is the model config.yaml names for its own endpoint, whichever
// provider is active. A UI compares it with Model() to explain that a /login
// selection overrides it, instead of silently showing one or the other.
func (s *Session) ConfigModel() string {
	return s.configProvider.Model
}

// ActiveEndpointConfigModel is the model the ACTIVE endpoint names for
// itself in config.yaml: ConfigModel's top-level model while the default
// endpoint (or a /login provider, which has no yaml model of its own) is
// active, or a named endpoint's own `model:` key while one of those is
// active. Unlike ConfigModel, which is hard-wired to the top-level block no
// matter which endpoint is running, this follows EndpointID — so a UI
// comparing the running model against "what config.yaml says" cites the
// right yaml value on a named endpoint instead of an unrelated top-level
// one. See tui/boot.go's bootModel and tui/settings.go's
// endpointOverrideNotice.
func (s *Session) ActiveEndpointConfigModel() string {
	id := s.EndpointID()
	if e, ok := s.endpoints.Lookup(id); ok && e.Kind == endpoint.KindNamed {
		if p, err := s.endpoints.Config(id); err == nil {
			return p.Model
		}
	}
	return s.ConfigModel()
}

// Provider returns the LLM transport the session is currently talking
// through ("openai", "codex", ...). Safe to call from another goroutine
// while a turn is running.
func (s *Session) Provider() string {
	s.modelMu.RLock()
	defer s.modelMu.RUnlock()
	return s.mainProvider.Provider
}

// ConfigProvider is the LLM transport config.yaml names for its own
// endpoint, whichever provider is active. Paired with Provider() the same
// way ConfigModel() is paired with Model(), so a UI can tell a /settings
// write to the "provider" key apart from what the session actually runs.
func (s *Session) ConfigProvider() string {
	return s.configProvider.Provider
}

// BaseURL returns the endpoint address the session is currently talking to.
// Safe to call from another goroutine while a turn is running.
func (s *Session) BaseURL() string {
	s.modelMu.RLock()
	defer s.modelMu.RUnlock()
	return s.mainProvider.BaseURL
}

// ConfigBaseURL is the endpoint address config.yaml names for its own
// endpoint, whichever provider is active. Paired with BaseURL() the same
// way ConfigModel() is paired with Model().
func (s *Session) ConfigBaseURL() string {
	return s.configProvider.BaseURL
}

// Providers lists every endpoint the pickers offer: the config.yaml default
// endpoint, its named endpoints, then the registry in its display order.
func (s *Session) Providers() []ProviderEntry {
	current := s.EndpointID()
	var out []ProviderEntry
	for _, e := range s.endpoints.List() {
		out = append(out, ProviderEntry{
			ID:           e.ID,
			Name:         e.Name,
			Kind:         e.Kind,
			LoginKind:    e.Def.LoginKind,
			Ready:        e.Ready,
			Stored:       e.Stored,
			Status:       e.Status,
			Current:      e.ID == current,
			NeedsBaseURL: e.NeedsBaseURL,
			EnvVar:       e.EnvVar,
			Model:        e.Model,
		})
	}
	return out
}

// ListProviderModels lists the models the given picker entry serves, so a UI
// can offer them before switching. llmprovider.ErrNoModelList means the
// provider has no listing nib can query: ask for a model name instead.
func (s *Session) ListProviderModels(ctx context.Context, id string) ([]string, error) {
	p, err := s.endpoints.Config(id)
	if err != nil {
		return nil, err
	}
	return s.listModels(ctx, p)
}

// ModelChoices lists the models of a picker entry ("" = the current provider)
// and whether the list is partial, in which case a UI should also accept a
// typed model name.
func (s *Session) ModelChoices(ctx context.Context, id string) ([]string, bool, error) {
	if id == "" {
		return s.modelChoices(ctx, s.resolvedSessionProvider())
	}
	p, err := s.endpoints.Config(id)
	if err != nil {
		return nil, false, err
	}
	return s.modelChoices(ctx, p)
}

// SwitchProvider points the session at an endpoint and model, rebuilding the
// LLM the way SetModel does. Conversation history is kept.
func (s *Session) SwitchProvider(id, model string) error {
	p, err := s.endpoints.Config(id)
	if err != nil {
		return err
	}
	if model = strings.TrimSpace(model); model != "" {
		p.Model = model
	}
	if p.Model == "" {
		return fmt.Errorf("no model selected for %s", id)
	}
	if err := s.applyProvider(p, id); err != nil {
		return err
	}
	// Persist the model the CALLER asked for, not p.Model (which SwitchProvider
	// has already resolved from the endpoint above). Every switch via
	// /endpoint, the picker, or /logout's return path passes model == "": a
	// bare switch must record NO model, so a later edit to config.yaml (or to
	// a named endpoint's own model:) stays authoritative on the next start
	// instead of being frozen out by what happened to be running right now.
	// Startup already falls back to the entry's own model when Saved.Model is
	// empty (endpoint.TestStartupSavedWithoutModelUsesTheEntryModel), so a
	// bare switch stays correct.
	return s.writeSaved(id, model)
}

// SaveModelAsDefault saves the model in use as the one the current endpoint
// starts on, in provider.json, so later sessions start there too. A /model
// pick alone does not do this (see SetModel); the /model picker calls this
// when the user picks with the save-as-default key.
func (s *Session) SaveModelAsDefault() error {
	model := s.Model()
	if model == "" {
		return fmt.Errorf("no model in use to save as the default")
	}
	return s.writeSaved(s.EndpointID(), model)
}

// writeSaved records an endpoint pick in provider.json with the fingerprint
// of the config as it is now, so the next start can tell a later config.yaml
// edit from an unchanged one (endpoint.Set.Reconcile). "Now" matters: the
// session's endpoint set is built once, at start, and config.yaml can change
// after that (/settings, another editor). A session loaded from a file
// therefore loads it again here, with the options it was loaded with
// (config.ReloadStartupEndpoint); a programmatic config registers none, has
// no file to change, and uses the set's own config.
func (s *Session) writeSaved(id, model string) error {
	sv := endpoint.Saved{ID: id, Model: model}
	if cfg, ok := config.ReloadStartupEndpoint(s.configRoot); ok {
		sv.ConfigFingerprint = endpoint.Fingerprint(cfg, sv)
	} else {
		sv.ConfigFingerprint = s.endpoints.Fingerprint(sv)
	}
	return endpoint.WriteSaved(s.savedPath, sv)
}

// SetDefaultModel is /model default [name]. An empty name saves the model in
// use as the endpoint's default. A name first switches this session to it,
// checked the way SwitchModel checks it, and nothing is saved when that
// fails. The notice is SwitchModel's for a switch, else "model: <name>".
func (s *Session) SetDefaultModel(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	notice := "model: " + s.Model()
	if name != "" && name != s.Model() {
		n, err := s.SwitchModel(ctx, name)
		if err != nil {
			return "", err
		}
		notice = n
	}
	if err := s.SaveModelAsDefault(); err != nil {
		return "", err
	}
	return notice, nil
}

// ProviderStateFile holds the endpoint picked via the picker or /login, next
// to credentials.json, so the next session starts on it. It is nib-managed
// state rather than a config.yaml edit: config.yaml keeps describing its own
// endpoints, which the picker's config.yaml entry switches back to.
const ProviderStateFile = "provider.json"

// restoreStartupEndpoint puts a new session on the endpoint the last one
// picked. A pick that no longer resolves leaves the session on the default
// and records why, for the boot log. The last change wins: a pick saved
// before config.yaml changed is dropped in favor of the config, with a note
// (endpoint.Set.Reconcile). IgnoreSavedEndpoint skips all of this and leaves
// provider.json alone.
func (s *Session) restoreStartupEndpoint() {
	if s.ignoreSavedEndpoint {
		return
	}
	sv, rewrite, dropped := s.endpoints.Reconcile(endpoint.LoadSaved(s.savedPath))
	if rewrite {
		var err error
		if sv == (endpoint.Saved{}) {
			err = endpoint.ClearSaved(s.savedPath)
		} else {
			err = endpoint.WriteSaved(s.savedPath, sv)
		}
		if err != nil {
			xlog.Warn("could not update the saved endpoint pick", "path", s.savedPath, "error", err)
		}
	}
	e, p, note := s.endpoints.Startup(sv)
	s.startupNote = note
	if dropped != "" {
		s.startupNote = dropped
	}
	if e.ID == endpoint.DefaultID && note == "" && p.Model == s.Model() {
		return // already built from the default, with the same model
	}
	if p.Model == "" {
		return
	}
	if err := s.applyProvider(p, e.ID); err != nil {
		xlog.Warn("could not start on the saved endpoint; using config.yaml", "endpoint", e.ID, "error", err)
	}
}

// restoreResumedModel puts a resumed session back on the endpoint and model
// it was using (types.Config.InitialEndpoint/InitialModel). Like a /model
// pick, it applies to this session only and saves nothing. A record from
// before sessions kept their endpoint (empty id) is left on the startup
// endpoint: its model name may belong to another provider. An endpoint that
// no longer resolves, or cannot authenticate, leaves the session on the
// startup endpoint too, and the boot log says why.
func (s *Session) restoreResumedModel(id, model string) {
	if id == "" || model == "" {
		return
	}
	if id == s.EndpointID() && model == s.Model() {
		return
	}
	skip := func(why string) {
		s.startupNote = fmt.Sprintf("could not resume on %s (%s): %s · using %s", id, model, why, s.Model())
	}
	e, ok := s.endpoints.Lookup(id)
	if !ok {
		skip("the endpoint is gone")
		return
	}
	if !e.Ready {
		skip("not logged in")
		return
	}
	p, err := s.endpoints.Config(id)
	if err != nil {
		skip(err.Error())
		return
	}
	p.Model = model
	if err := s.applyProvider(p, id); err != nil {
		skip(err.Error())
	}
}

// SaveAPIKey stores an API-key login for a provider (the TUI's key form).
// baseURL is only kept for providers that need one (ProviderEntry.NeedsBaseURL).
func (s *Session) SaveAPIKey(id, key, baseURL string) (auth.Credential, error) {
	def, ok := provider.Get(id)
	if !ok {
		return auth.Credential{}, fmt.Errorf("unknown provider %q", id)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return auth.Credential{}, fmt.Errorf("the API key is empty")
	}
	if !def.NeedsBaseURL() {
		baseURL = ""
	}
	return auth.LoginAPIKeyAt(s.credStore, def, key, baseURL)
}

// AddEndpoint persists a named endpoint to config.yaml via the Configurator
// and flags the session for reload so the new endpoint appears immediately in
// the /endpoint picker.
func (s *Session) AddEndpoint(name string, cfg types.ModelProviderConfig) error {
	if err := s.configurator.AddEndpoint(name, cfg); err != nil {
		return err
	}
	s.requestReload()
	return nil
}

// RemoveEndpoint deletes a named endpoint from config.yaml and flags the
// session for reload.
func (s *Session) RemoveEndpoint(name string) error {
	if err := s.configurator.RemoveEndpoint(name); err != nil {
		return err
	}
	s.requestReload()
	return nil
}
