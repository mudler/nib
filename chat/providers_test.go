package chat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/endpoint"
	"github.com/mudler/nib/llmprovider"
	"github.com/mudler/nib/provider"
	"github.com/mudler/nib/types"
)

// newProviderSession builds a Session directly (bypassing NewSession, which
// also stands up MCP clients, tracing, hooks and an endpoint probe) around a
// single ModelProviderConfig, for tests that only exercise the provider
// picker. Persistence (provider.json) is not wired up: a test that needs it
// either sets s.savedPath directly or uses newTestSessionWithConfig with a
// BaseDir.
func newProviderSession(t *testing.T, cfg types.ModelProviderConfig) *Session {
	t.Helper()
	return newTestSessionWithConfig(t, types.Config{
		Provider: cfg.Provider,
		Model:    cfg.Model,
		APIKey:   cfg.APIKey,
		BaseURL:  cfg.BaseURL,
	})
}

// newTestSessionWithConfig builds a Session the same minimal way, around a
// full types.Config — so it also resolves any named endpoints config.yaml
// declares. Persistence always gets a writable directory: cfg.BaseDir when
// set (so two sessions built from the same cfg see each other's saved pick,
// the way two real nib runs over the same state directory would), otherwise
// a fresh t.TempDir() private to this session — mirroring NewSession, where
// plugin.BaseDirIn(cfg.BaseDir) never actually resolves to "".
func newTestSessionWithConfig(t *testing.T, cfg types.Config) *Session {
	t.Helper()
	stateDir := cfg.BaseDir
	if stateDir == "" {
		stateDir = t.TempDir()
	}
	credStore := auth.NewStore(filepath.Join(stateDir, "credentials.json"))
	endpoints, errs := endpoint.New(cfg, credStore)
	if len(errs) != 0 {
		t.Fatalf("endpoint.New: %v", errs)
	}
	main := cfg.ResolvedMainModel()
	savedPath := filepath.Join(stateDir, ProviderStateFile)
	return &Session{
		ctx:            context.Background(),
		llmModel:       main.Model,
		mainProvider:   main,
		configProvider: main,
		endpoints:      endpoints,
		endpointID:     ConfigProviderID,
		savedPath:      savedPath,
		credStore:      credStore,

		ignoreSavedEndpoint: cfg.IgnoreSavedEndpoint,
	}
}

func TestProvidersReportLoginState(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "gsk-env")
	s := newProviderSession(t, types.ModelProviderConfig{Provider: "openai", Model: "local", BaseURL: "http://localhost:8080/v1"})
	if _, err := s.SaveAPIKey("regolo", "  rg-secret-1234 ", "ignored"); err != nil {
		t.Fatal(err)
	}

	byID := map[string]ProviderEntry{}
	for _, e := range s.Providers() {
		byID[e.ID] = e
	}
	// endpoint.describe() renders "model @ host[:port]" (a short host for the
	// picker's status column), not the full base URL.
	if e := byID[ConfigProviderID]; !e.Current || !e.Ready || e.Status != "local @ localhost:8080" {
		t.Fatalf("config entry = %+v", e)
	}
	if e := byID["regolo"]; !e.Stored || !e.Ready || e.Current {
		t.Fatalf("regolo entry = %+v", e)
	}
	if e := byID["groq"]; e.Stored || !e.Ready {
		t.Fatalf("groq (env key) entry = %+v", e)
	}
	if e := byID["mistral"]; e.Ready {
		t.Fatalf("mistral (no key) entry = %+v", e)
	}
	if !byID["azure"].NeedsBaseURL || byID["regolo"].NeedsBaseURL {
		t.Fatal("only providers without a default endpoint should ask for a base URL")
	}

	// The base URL is kept only where the provider needs one.
	c, _, _ := s.credStore.Get("regolo")
	if c.APIKey != "rg-secret-1234" || c.BaseURL != "" {
		t.Fatalf("stored regolo credential = %+v", c)
	}
}

func TestSwitchProviderAndBack(t *testing.T) {
	srv, requests := newLLMServer(t)
	s := newProviderSession(t, types.ModelProviderConfig{Provider: "openai", Model: "local", BaseURL: srv.URL + "/v1", APIKey: "local-key"})
	if _, err := s.SaveAPIKey("regolo", "rg-key", ""); err != nil {
		t.Fatal(err)
	}

	if err := s.SwitchProvider("regolo", ""); err == nil {
		t.Fatal("switching without a model must fail rather than keep a model the new provider does not serve")
	}
	if err := s.SwitchProvider("regolo", "Llama-3.3-70B-Instruct"); err != nil {
		t.Fatal(err)
	}
	if s.ProviderID() != "regolo" || s.Model() != "Llama-3.3-70B-Instruct" {
		t.Fatalf("after switch: provider=%q model=%q", s.ProviderID(), s.Model())
	}
	p := s.resolvedSessionProvider()
	if p.Provider != "regolo" || p.BaseURL != "" || p.APIKey != "" {
		t.Fatalf("regolo config = %+v, want the registry endpoint with the stored key", p)
	}
	if base, key, _ := llmprovider.ModelsEndpoint(p, s.credStore); key != "rg-key" || base != "https://api.regolo.ai/v1" {
		t.Fatalf("regolo endpoint = %q key %q", base, key)
	}

	// Back to config.yaml: the configured endpoint and key, and its model.
	if err := s.SwitchProvider(ConfigProviderID, ""); err != nil {
		t.Fatal(err)
	}
	if s.ProviderID() != ConfigProviderID || s.Model() != "local" {
		t.Fatalf("after switching back: provider=%q model=%q", s.ProviderID(), s.Model())
	}
	askOnce(t, s.llm)
	if reqs := requests(); len(reqs) != 1 || reqs[0].Model != "local" {
		t.Fatalf("requests after switching back = %+v", reqs)
	}
}

func TestSwitchToANamedEndpoint(t *testing.T) {
	s := newTestSessionWithConfig(t, types.Config{
		Model: "default-model", BaseURL: "http://localhost:8080/v1",
		Endpoints: types.Endpoints{{
			Name:                "work",
			ModelProviderConfig: types.ModelProviderConfig{BaseURL: "https://vllm.corp/v1", Model: "llama"},
		}},
	})
	if err := s.SwitchProvider("@work", ""); err != nil {
		t.Fatalf("SwitchProvider: %v", err)
	}
	if got := s.EndpointID(); got != "@work" {
		t.Fatalf("EndpointID = %q", got)
	}
	if got := s.Model(); got != "llama" {
		t.Fatalf("Model = %q, want the endpoint's model", got)
	}
}

// A bare switch (empty model — what /endpoint, the picker, and /logout's
// return path always pass) must record NO model in provider.json, so an edit
// to config.yaml's model between sessions wins on the next start. Before this
// fix, SwitchProvider persisted the model it had just resolved FROM the
// endpoint, which froze that model into provider.json and made a later
// config.yaml edit inert — exactly the "write the file, nothing happens" bug
// named endpoints exist to eliminate.
func TestBareSwitchToTheDefaultEndpointLeavesConfigYAMLAuthoritative(t *testing.T) {
	cfg := types.Config{BaseDir: t.TempDir(), Model: "old-model", BaseURL: "http://localhost:8080/v1"}
	first := newTestSessionWithConfig(t, cfg)
	if err := first.SwitchProvider(ConfigProviderID, ""); err != nil {
		t.Fatal(err)
	}

	edited := cfg
	edited.Model = "new-model"
	second := newTestSessionWithConfig(t, edited)
	second.restoreStartupEndpoint()
	if got := second.Model(); got != "new-model" {
		t.Fatalf("Model = %q, want the edited config.yaml model (new-model) to win, not the model frozen at switch time", got)
	}
}

// The named-endpoint sibling of the test above: a bare `/endpoint @work`
// must not freeze @work's model either.
func TestBareSwitchToANamedEndpointLeavesConfigYAMLAuthoritative(t *testing.T) {
	cfg := types.Config{
		BaseDir: t.TempDir(),
		Model:   "default-model", BaseURL: "http://localhost:8080/v1",
		Endpoints: types.Endpoints{{
			Name:                "work",
			ModelProviderConfig: types.ModelProviderConfig{BaseURL: "https://vllm.corp/v1", Model: "old-work-model"},
		}},
	}
	first := newTestSessionWithConfig(t, cfg)
	if err := first.SwitchProvider("@work", ""); err != nil {
		t.Fatal(err)
	}

	edited := cfg
	edited.Endpoints = types.Endpoints{{
		Name:                "work",
		ModelProviderConfig: types.ModelProviderConfig{BaseURL: "https://vllm.corp/v1", Model: "new-work-model"},
	}}
	second := newTestSessionWithConfig(t, edited)
	second.restoreStartupEndpoint()
	if got := second.EndpointID(); got != "@work" {
		t.Fatalf("EndpointID = %q, want @work restored", got)
	}
	if got := second.Model(); got != "new-work-model" {
		t.Fatalf("Model = %q, want the edited endpoint model (new-work-model) to win, not the model frozen at switch time", got)
	}
}

func TestANamedEndpointIsRestoredOnTheNextSession(t *testing.T) {
	cfg := types.Config{
		BaseDir: t.TempDir(),
		Model:   "default-model", BaseURL: "http://localhost:8080/v1",
		Endpoints: types.Endpoints{{
			Name:                "work",
			ModelProviderConfig: types.ModelProviderConfig{BaseURL: "https://vllm.corp/v1", Model: "llama"},
		}},
	}
	first := newTestSessionWithConfig(t, cfg)
	if err := first.SwitchProvider("@work", "llama-70b"); err != nil {
		t.Fatalf("SwitchProvider: %v", err)
	}
	second := newTestSessionWithConfig(t, cfg)
	second.restoreStartupEndpoint()
	if got := second.EndpointID(); got != "@work" {
		t.Fatalf("EndpointID = %q, want the saved pick restored", got)
	}
	if got := second.Model(); got != "llama-70b" {
		t.Fatalf("Model = %q, want the saved model", got)
	}
}

// A /model pick belongs to the session that made it. It must not reach
// provider.json: every nib process reads that file at startup, so a pick
// written there would switch every other session started afterwards.
func TestPickingAModelStaysInTheSession(t *testing.T) {
	cfg := types.Config{BaseDir: t.TempDir(), Model: "default-model", BaseURL: "http://localhost:8080/v1"}
	first := newTestSessionWithConfig(t, cfg)
	if err := first.SetModel("other-model"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if got := first.Model(); got != "other-model" {
		t.Fatalf("Model = %q, want the pick applied to this session", got)
	}
	second := newTestSessionWithConfig(t, cfg)
	second.restoreStartupEndpoint()
	if got := second.Model(); got != "default-model" {
		t.Fatalf("Model = %q, want config.yaml's model: a /model pick must not reach other sessions", got)
	}
	if got := second.EndpointID(); got != endpoint.DefaultID {
		t.Fatalf("EndpointID = %q", got)
	}
}

// A /model pick on a saved endpoint keeps the endpoint pick as it was.
func TestPickingAModelKeepsTheSavedEndpointPick(t *testing.T) {
	cfg := types.Config{
		BaseDir: t.TempDir(),
		Model:   "default-model", BaseURL: "http://localhost:8080/v1",
		Endpoints: types.Endpoints{{
			Name:                "work",
			ModelProviderConfig: types.ModelProviderConfig{BaseURL: "https://vllm.corp/v1", Model: "llama"},
		}},
	}
	first := newTestSessionWithConfig(t, cfg)
	if err := first.SwitchProvider("@work", "llama-70b"); err != nil {
		t.Fatalf("SwitchProvider: %v", err)
	}
	if err := first.SetModel("llama-8b"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	second := newTestSessionWithConfig(t, cfg)
	second.restoreStartupEndpoint()
	if got := second.EndpointID(); got != "@work" {
		t.Fatalf("EndpointID = %q, want the saved endpoint pick", got)
	}
	if got := second.Model(); got != "llama-70b" {
		t.Fatalf("Model = %q, want the model saved with the endpoint pick, not the session's /model", got)
	}
}

// /model default saves a model for later sessions: with a name it switches
// to it first, without one it saves the model in use.
func TestSetDefaultModelSavesForLaterSessions(t *testing.T) {
	cfg := types.Config{BaseDir: t.TempDir(), Model: "default-model", BaseURL: "http://127.0.0.1:1/v1"}
	first := newTestSessionWithConfig(t, cfg)
	if _, err := first.SetDefaultModel(context.Background(), "other-model"); err != nil {
		t.Fatalf("SetDefaultModel: %v", err)
	}
	if got := first.Model(); got != "other-model" {
		t.Fatalf("Model = %q, want the named model applied to this session", got)
	}
	second := newTestSessionWithConfig(t, cfg)
	second.restoreStartupEndpoint()
	if got := second.Model(); got != "other-model" {
		t.Fatalf("Model = %q, want the saved default", got)
	}

	if err := second.SetModel("third-model"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if _, err := second.SetDefaultModel(context.Background(), ""); err != nil {
		t.Fatalf("SetDefaultModel without a name: %v", err)
	}
	third := newTestSessionWithConfig(t, cfg)
	third.restoreStartupEndpoint()
	if got := third.Model(); got != "third-model" {
		t.Fatalf("Model = %q, want the model in use saved", got)
	}
}

func TestResetModelRestoresTheEndpointModel(t *testing.T) {
	cfg := types.Config{BaseDir: t.TempDir(), Model: "default-model", BaseURL: "http://localhost:8080/v1"}
	s := newTestSessionWithConfig(t, cfg)
	if err := s.SetModel("other-model"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	got, err := s.ResetModel()
	if err != nil {
		t.Fatalf("ResetModel: %v", err)
	}
	if got != "default-model" || s.Model() != "default-model" {
		t.Fatalf("ResetModel = %q, Model = %q", got, s.Model())
	}
}

func TestResetModelOnANamedEndpointRestoresItsOwnModel(t *testing.T) {
	cfg := types.Config{
		BaseDir: t.TempDir(),
		Model:   "default-model", BaseURL: "http://localhost:8080/v1",
		Endpoints: types.Endpoints{{
			Name:                "work",
			ModelProviderConfig: types.ModelProviderConfig{BaseURL: "https://vllm.corp/v1", Model: "llama"},
		}},
	}
	s := newTestSessionWithConfig(t, cfg)
	if err := s.SwitchProvider("@work", "llama-70b"); err != nil {
		t.Fatalf("SwitchProvider: %v", err)
	}
	got, err := s.ResetModel()
	if err != nil {
		t.Fatalf("ResetModel: %v", err)
	}
	if got != "llama" || s.Model() != "llama" {
		t.Fatalf("ResetModel = %q, Model = %q, want @work's own model (llama)", got, s.Model())
	}
	if s.EndpointID() != "@work" {
		t.Fatalf("EndpointID = %q, want the endpoint kept, only the override dropped", s.EndpointID())
	}
}

func TestResetModelErrorsWhenTheEndpointNamesNoModel(t *testing.T) {
	s := newTestSessionWithConfig(t, types.Config{Model: "default-model", BaseURL: "http://localhost:8080/v1"})
	if _, err := s.SaveAPIKey("regolo", "rg-key", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SwitchProvider("regolo", "Llama-3.3-70B-Instruct"); err != nil {
		t.Fatalf("SwitchProvider: %v", err)
	}
	if _, err := s.ResetModel(); err == nil {
		t.Fatal("want an error: regolo names no model of its own")
	}
	// The failed reset must not have disturbed the current model.
	if s.Model() != "Llama-3.3-70B-Instruct" {
		t.Fatalf("Model = %q, want the pick left alone after a failed reset", s.Model())
	}
}

func TestListProviderModelsWithoutListing(t *testing.T) {
	s := newProviderSession(t, types.ModelProviderConfig{Provider: "openai", Model: "local", BaseURL: "http://unused.invalid/v1"})
	// Azure's adapter has no model listing (deployments are per account).
	t.Setenv("AZURE_OPENAI_API_KEY", "az-key")
	t.Setenv("AZURE_OPENAI_BASE_URL", "https://example.openai.azure.com/openai/v1")
	if _, err := s.ListProviderModels(context.Background(), "azure"); !errors.Is(err, llmprovider.ErrNoModelList) {
		t.Fatalf("azure listing err = %v, want ErrNoModelList", err)
	}
	if _, err := s.ListProviderModels(context.Background(), "nope"); err == nil {
		t.Fatal("unknown provider was accepted")
	}
}

func TestSwitchProviderPersistsTheDefault(t *testing.T) {
	cfg := types.Config{BaseDir: t.TempDir(), Provider: "openai", Model: "local", BaseURL: "http://unused.invalid/v1"}
	s := newTestSessionWithConfig(t, cfg)
	if _, err := s.SaveAPIKey("regolo", "rg-key", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SwitchProvider("regolo", "model-one"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModel("model-two"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveModelAsDefault(); err != nil { // the picker's ctrl+s, /model default
		t.Fatal(err)
	}

	// A fresh session over the same state directory starts where the last
	// one left off.
	next := newTestSessionWithConfig(t, cfg)
	next.restoreStartupEndpoint()
	if next.ProviderID() != "regolo" || next.Model() != "model-two" {
		t.Fatalf("restored provider=%q model=%q, want regolo/model-two", next.ProviderID(), next.Model())
	}

	// Picking config.yaml again forgets the default.
	if err := next.SwitchProvider(ConfigProviderID, ""); err != nil {
		t.Fatal(err)
	}
	t.Run("recordsTheDefaultInstead", func(t *testing.T) {
		// Every endpoint's pick is saved uniformly now, including the
		// config.yaml default: switching back to it no longer removes
		// provider.json, it records {"id":"config"}. The call above passes
		// model == "" (a bare switch, the shape /endpoint, the picker and
		// /logout's return path all use), and SwitchProvider records NO
		// model for a bare switch — never the model it happened to resolve
		// from the endpoint — precisely so config.yaml stays authoritative:
		// an edit to config.yaml's model between sessions must win on the
		// next start rather than being frozen out by whatever was running
		// when the switch happened (see TestBareSwitchToTheDefaultEndpoint
		// LeavesConfigYAMLAuthoritative and its named-endpoint sibling).
		if _, err := os.Stat(next.savedPath); err != nil {
			t.Fatalf("provider.json should still exist after switching back, stat err=%v", err)
		}
		sv := endpoint.LoadSaved(next.savedPath)
		if sv.ID != ConfigProviderID {
			t.Fatalf("Saved = %#v, want ID %q", sv, ConfigProviderID)
		}
		if sv.Model != "" && sv.Model != "local" {
			t.Fatalf("Saved.Model = %q, want empty or the default's own model (local)", sv.Model)
		}
		fresh := newTestSessionWithConfig(t, cfg)
		fresh.restoreStartupEndpoint()
		if fresh.ProviderID() != ConfigProviderID || fresh.Model() != "local" {
			t.Fatalf("with the default recorded: provider=%q model=%q", fresh.ProviderID(), fresh.Model())
		}
	})
}

func TestRestoreIgnoresUnknownProvider(t *testing.T) {
	cfg := types.Config{BaseDir: t.TempDir(), Provider: "openai", Model: "local", BaseURL: "http://unused.invalid/v1"}
	s := newTestSessionWithConfig(t, cfg)
	if err := endpoint.WriteSaved(s.savedPath, endpoint.Saved{ID: "gone-provider", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	s.restoreStartupEndpoint()
	if s.EndpointID() != ConfigProviderID || s.Model() != "local" {
		t.Fatalf("an unknown saved provider must leave config.yaml in charge: %q/%q", s.EndpointID(), s.Model())
	}
}

func TestSwitchModelHonoursPartialLists(t *testing.T) {
	t.Setenv("AZURE_OPENAI_API_KEY", "az-key")
	t.Setenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", "gpt-4.1=prod")
	s := newProviderSession(t, types.ModelProviderConfig{Provider: "azure", Model: "gpt-4.1", BaseURL: "https://example.openai.azure.com/openai/v1"})

	// A deployment the map does not name is still accepted: the list is a
	// suggestion, and refusing it would lock the user out of it.
	notice, err := s.SwitchModel(context.Background(), "other-deployment")
	if err != nil || s.Model() != "other-deployment" || !strings.Contains(notice, "suggested") {
		t.Fatalf("partial list: notice=%q err=%v model=%q", notice, err, s.Model())
	}

	ids, partial, err := s.ModelChoices(context.Background(), "")
	if err != nil || !partial || len(ids) != 1 {
		t.Fatalf("ModelChoices = %v, %v, %v", ids, partial, err)
	}
}

// SwitchModel validates the name and then calls SetModel, whose own error
// (a client the provider factory could not build) is a DIFFERENT failure
// than an unserved name. Every branch of SwitchModel's switch used to call
// SetModel and discard that error, unconditionally returning a success
// notice — a rebuild failure was reported to the user as a switch that
// worked. A Session literal with an unrecognized provider ID is the cheapest
// way to make SetModel's rebuild fail deterministically, with no network
// call: ModelsEndpoint (behind the lookup SwitchModel does first) and
// llmprovider.NewWithStore (behind SetModel) both fail at the provider.Get
// lookup, before either would dial anything.
func TestSwitchModelReportsASetModelFailureInsteadOfSuccess(t *testing.T) {
	s := &Session{
		llmModel: "model-a",
		mainProvider: types.ModelProviderConfig{
			Provider: "not-a-real-provider",
			Model:    "model-a",
		},
	}

	notice, err := s.SwitchModel(context.Background(), "model-b")
	if err == nil {
		t.Fatalf("SwitchModel returned notice %q, want an error: the provider factory cannot build a client for an unrecognized provider", notice)
	}
	if notice != "" {
		t.Fatalf("SwitchModel returned notice %q alongside an error, want it empty on failure", notice)
	}
	if got := s.Model(); got != "model-a" {
		t.Fatalf("Model() = %q after a failed switch, want it left at model-a", got)
	}
}

// The TUI's boot log, header and /model picker all name the provider the
// session really talks to. A saved /login pick overrides config.yaml's model
// at startup, so reading config.yaml there showed a model no request used.
func TestActiveProviderNameFollowsTheLogin(t *testing.T) {
	s := newProviderSession(t, types.ModelProviderConfig{Provider: "openai", Model: "uncensored", BaseURL: "http://localhost:8080/v1"})

	if got := s.ActiveProviderName(); got != "config.yaml" {
		t.Fatalf("ActiveProviderName on config.yaml = %q, want config.yaml", got)
	}
	if got := s.ConfigModel(); got != "uncensored" {
		t.Fatalf("ConfigModel = %q, want uncensored", got)
	}
	if _, err := s.SaveAPIKey("regolo", "rg-secret", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SwitchProvider("regolo", "glm5.2"); err != nil {
		t.Fatal(err)
	}
	if got := s.ActiveProviderName(); got != "Regolo" {
		t.Fatalf("ActiveProviderName after /login = %q, want Regolo", got)
	}
	if got := s.ConfigModel(); got != "uncensored" {
		t.Fatalf("ConfigModel after /login = %q, want config.yaml's model unchanged", got)
	}
	if got := s.Model(); got != "glm5.2" {
		t.Fatalf("Model = %q, want glm5.2", got)
	}
}

func TestFormatProviderModelListNamesTheProvider(t *testing.T) {
	got := FormatProviderModelList("Regolo", []string{"a", "b"}, "b")
	want := "Regolo models:\n  a\n* b\n"
	if got != want {
		t.Fatalf("FormatProviderModelList = %q, want %q", got, want)
	}
}

func TestLogoutOfTheActiveProviderReturnsToTheDefault(t *testing.T) {
	s := newTestSessionWithConfig(t, types.Config{Model: "default-model", BaseURL: "http://localhost:8080/v1"})
	if err := s.credStore.Save(auth.Credential{
		ProviderID: "anthropic", Kind: auth.CredentialAPIKey, APIKey: "k",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.SwitchProvider("anthropic", "claude-opus-5"); err != nil {
		t.Fatalf("SwitchProvider: %v", err)
	}
	notice, err := s.Logout("anthropic")
	if err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if s.EndpointID() != endpoint.DefaultID {
		t.Fatalf("EndpointID = %q, want the default after logging out of the active provider", s.EndpointID())
	}
	if s.Model() != "default-model" {
		t.Fatalf("Model = %q", s.Model())
	}
	if !strings.Contains(notice, "config.yaml") {
		t.Fatalf("notice = %q, want it to name the endpoint it fell back to", notice)
	}
}

// Finding 4: when the credential-deleting logout's return switch to
// config.yaml itself fails (here: config.yaml names no model), the session
// is left stranded ON the just-logged-out-of provider with no login. The
// notice must say so — not the plain "Logged out of X" that leaves the user
// believing they are safely back on config.yaml.
func TestLogoutStrandedWhenTheReturnSwitchFails(t *testing.T) {
	s := newTestSessionWithConfig(t, types.Config{BaseURL: "http://localhost:8080/v1"}) // no model: the return switch will fail
	if err := s.credStore.Save(auth.Credential{
		ProviderID: "anthropic", Kind: auth.CredentialAPIKey, APIKey: "k",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.SwitchProvider("anthropic", "claude-opus-5"); err != nil {
		t.Fatalf("SwitchProvider: %v", err)
	}
	def, ok := provider.Get("anthropic")
	if !ok {
		t.Fatal("anthropic is not registered")
	}

	notice, err := s.Logout("anthropic")
	if err != nil {
		t.Fatalf("Logout: %v", err)
	}
	// The logout itself succeeded: the credential is gone.
	if _, exists, _ := s.credStore.Get("anthropic"); exists {
		t.Fatal("credential was not deleted")
	}
	// The return switch failed, so the session is still on anthropic.
	if s.EndpointID() != "anthropic" {
		t.Fatalf("EndpointID = %q, want the session left stranded on anthropic", s.EndpointID())
	}
	if !strings.Contains(notice, "Logged out of "+def.Name) {
		t.Fatalf("notice = %q, want the logout itself still reported as successful", notice)
	}
	if !strings.Contains(notice, "still on "+def.Name) {
		t.Fatalf("notice = %q, want it to name the stranded state", notice)
	}
	if !strings.Contains(notice, "/endpoint") {
		t.Fatalf("notice = %q, want it to point at /endpoint to pick another", notice)
	}
}

func TestLogoutOfAnotherProviderDoesNotSwitch(t *testing.T) {
	s := newTestSessionWithConfig(t, types.Config{Model: "default-model", BaseURL: "http://localhost:8080/v1"})
	for _, id := range []string{"anthropic", "openai"} {
		if err := s.credStore.Save(auth.Credential{ProviderID: id, Kind: auth.CredentialAPIKey, APIKey: "k"}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	if err := s.SwitchProvider("anthropic", "claude-opus-5"); err != nil {
		t.Fatalf("SwitchProvider: %v", err)
	}
	if _, err := s.Logout("openai"); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if s.EndpointID() != "anthropic" {
		t.Fatalf("EndpointID = %q, want the session left alone", s.EndpointID())
	}
}

// A resumed session carries on with the model it was using, on the endpoint
// it was using, whatever the saved default is.
func TestResumeRestoresTheSessionModel(t *testing.T) {
	cfg := types.Config{
		BaseDir: t.TempDir(),
		Model:   "default-model", BaseURL: "http://localhost:8080/v1",
		Endpoints: types.Endpoints{{
			Name:                "work",
			ModelProviderConfig: types.ModelProviderConfig{BaseURL: "https://vllm.corp/v1", Model: "llama"},
		}},
	}
	s := newTestSessionWithConfig(t, cfg)
	s.restoreStartupEndpoint()
	s.restoreResumedModel("@work", "llama-8b")
	if got := s.EndpointID(); got != "@work" {
		t.Fatalf("EndpointID = %q, want the resumed session's endpoint", got)
	}
	if got := s.Model(); got != "llama-8b" {
		t.Fatalf("Model = %q, want the resumed session's model", got)
	}
	if note := s.StartupNote(); note != "" {
		t.Fatalf("StartupNote = %q, want none", note)
	}

	// Restoring is a session pick, not a saved default.
	next := newTestSessionWithConfig(t, cfg)
	next.restoreStartupEndpoint()
	if got := next.Model(); got != "default-model" {
		t.Fatalf("next session Model = %q, want config.yaml's model", got)
	}
}

// A record from before sessions kept their endpoint, or one whose endpoint
// is gone, leaves the session on the startup endpoint and says why.
func TestResumeKeepsTheStartupModelWhenTheEndpointIsGone(t *testing.T) {
	cfg := types.Config{BaseDir: t.TempDir(), Model: "default-model", BaseURL: "http://localhost:8080/v1"}
	s := newTestSessionWithConfig(t, cfg)
	s.restoreResumedModel("", "old-model")
	if got := s.Model(); got != "default-model" {
		t.Fatalf("Model = %q, want the startup model for a record without an endpoint", got)
	}
	s.restoreResumedModel("@gone", "gone-model")
	if got := s.Model(); got != "default-model" {
		t.Fatalf("Model = %q, want the startup model when the endpoint is gone", got)
	}
	if note := s.StartupNote(); !strings.Contains(note, "@gone") || !strings.Contains(note, "gone-model") {
		t.Fatalf("StartupNote = %q, want it to name the endpoint and model it could not restore", note)
	}
}
