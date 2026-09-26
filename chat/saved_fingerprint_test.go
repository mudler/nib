package chat

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/mudler/nib/endpoint"
	"github.com/mudler/nib/types"
)

func regoloPickConfig(t *testing.T) types.Config {
	t.Helper()
	return types.Config{BaseDir: t.TempDir(), Model: "uncensored", BaseURL: "http://127.0.0.1:1/v1"}
}

func pickRegolo(t *testing.T, cfg types.Config) {
	t.Helper()
	s := newTestSessionWithConfig(t, cfg)
	if _, err := s.SaveAPIKey("regolo", "rg-secret", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SwitchProvider("regolo", "glm5.2"); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func TestSavedPickRecordsTheConfigFingerprint(t *testing.T) {
	cfg := regoloPickConfig(t)
	s := newTestSessionWithConfig(t, cfg)
	if _, err := s.SaveAPIKey("regolo", "rg-secret", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SwitchProvider("regolo", "glm5.2"); err != nil {
		t.Fatal(err)
	}
	sv := endpoint.LoadSaved(s.savedPath)
	if sv.ConfigFingerprint == "" || sv.ConfigFingerprint != s.endpoints.Fingerprint(sv) {
		t.Fatalf("Saved = %+v, want the fingerprint of the config at pick time", sv)
	}
}

func TestStartupWithAMatchingFingerprintKeepsThePick(t *testing.T) {
	cfg := regoloPickConfig(t)
	pickRegolo(t, cfg)
	s := newTestSessionWithConfig(t, cfg)
	before := readFile(t, s.savedPath)
	s.restoreStartupEndpoint()
	if s.EndpointID() != "regolo" || s.Model() != "glm5.2" {
		t.Fatalf("started on %q/%q, want the saved regolo/glm5.2", s.EndpointID(), s.Model())
	}
	if note := s.StartupNote(); note != "" {
		t.Fatalf("StartupNote = %q, want none", note)
	}
	if after := readFile(t, s.savedPath); after != before {
		t.Fatalf("provider.json changed:\n%s\n->\n%s", before, after)
	}
}

func TestStartupAfterAConfigEditStartsOnTheConfigOnce(t *testing.T) {
	cfg := regoloPickConfig(t)
	pickRegolo(t, cfg)

	edited := cfg
	edited.BaseURL = "http://localhost:8080/v1"
	edited.Model = "local-model"
	s := newTestSessionWithConfig(t, edited)
	s.restoreStartupEndpoint()
	if s.EndpointID() != ConfigProviderID || s.Model() != "local-model" || s.BaseURL() != "http://localhost:8080/v1" {
		t.Fatalf("started on %q/%q@%q, want config.yaml's local-model", s.EndpointID(), s.Model(), s.BaseURL())
	}
	note := s.StartupNote()
	for _, want := range []string{"config.yaml changed", "regolo", "glm5.2"} {
		if !strings.Contains(note, want) {
			t.Fatalf("StartupNote = %q, missing %q", note, want)
		}
	}
	if sv := endpoint.LoadSaved(s.savedPath); sv.ID != "" || sv.Model != "" {
		t.Fatalf("provider.json still holds %+v, want the pick dropped", sv)
	}

	again := newTestSessionWithConfig(t, edited)
	again.restoreStartupEndpoint()
	if note := again.StartupNote(); note != "" {
		t.Fatalf("second start StartupNote = %q, want the note shown once", note)
	}
	if again.EndpointID() != ConfigProviderID || again.Model() != "local-model" {
		t.Fatalf("second start on %q/%q", again.EndpointID(), again.Model())
	}
}

// ctrl+s / /model default is the last change, so it sticks; a later edit to
// the endpoint's model in config.yaml is the last change after that, so it
// wins.
func TestSavedDefaultModelThenConfigEditOfThatEndpoint(t *testing.T) {
	cfg := types.Config{
		BaseDir: t.TempDir(),
		Model:   "default-model", BaseURL: "http://localhost:8080/v1",
		Endpoints: types.Endpoints{{
			Name:                "work",
			ModelProviderConfig: types.ModelProviderConfig{BaseURL: "https://vllm.corp/v1", Model: "llama"},
		}},
	}
	first := newTestSessionWithConfig(t, cfg)
	if err := first.SwitchProvider("@work", ""); err != nil {
		t.Fatal(err)
	}
	if err := first.SetModel("llama-70b"); err != nil {
		t.Fatal(err)
	}
	if err := first.SaveModelAsDefault(); err != nil { // ctrl+s in the /model picker
		t.Fatal(err)
	}

	second := newTestSessionWithConfig(t, cfg)
	second.restoreStartupEndpoint()
	if second.EndpointID() != "@work" || second.Model() != "llama-70b" || second.StartupNote() != "" {
		t.Fatalf("unchanged restart on %q/%q note=%q, want @work/llama-70b and no note", second.EndpointID(), second.Model(), second.StartupNote())
	}

	edited := cfg
	edited.Endpoints = types.Endpoints{{
		Name:                "work",
		ModelProviderConfig: types.ModelProviderConfig{BaseURL: "https://vllm.corp/v1", Model: "llama-3"},
	}}
	third := newTestSessionWithConfig(t, edited)
	third.restoreStartupEndpoint()
	if third.Model() == "llama-70b" {
		t.Fatalf("Model = llama-70b, want the edited config to win over the saved model")
	}
	if third.EndpointID() != ConfigProviderID || third.Model() != "default-model" {
		t.Fatalf("started on %q/%q, want config.yaml's default", third.EndpointID(), third.Model())
	}
	if note := third.StartupNote(); !strings.Contains(note, "@work") || !strings.Contains(note, "llama-70b") {
		t.Fatalf("StartupNote = %q, want it to name @work and llama-70b", note)
	}
}

func TestSavedDefaultModelOnTheDefaultEndpointThenConfigModelEdit(t *testing.T) {
	cfg := types.Config{BaseDir: t.TempDir(), Model: "default-model", BaseURL: "http://127.0.0.1:1/v1"}
	first := newTestSessionWithConfig(t, cfg)
	if _, err := first.SetDefaultModel(context.Background(), "other-model"); err != nil {
		t.Fatal(err)
	}
	edited := cfg
	edited.Model = "edited-model"
	second := newTestSessionWithConfig(t, edited)
	second.restoreStartupEndpoint()
	if second.Model() != "edited-model" {
		t.Fatalf("Model = %q, want the edited config.yaml model", second.Model())
	}
	if !strings.Contains(second.StartupNote(), "other-model") {
		t.Fatalf("StartupNote = %q, want it to name the dropped model", second.StartupNote())
	}
}

func TestLegacySavedPickSticksAndIsBackfilled(t *testing.T) {
	cfg := regoloPickConfig(t)
	pickRegolo(t, cfg) // stores the regolo credential
	s := newTestSessionWithConfig(t, cfg)
	if err := os.WriteFile(s.savedPath, []byte(`{"id":"regolo","model":"glm5.2"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s.restoreStartupEndpoint()
	if s.EndpointID() != "regolo" || s.Model() != "glm5.2" {
		t.Fatalf("started on %q/%q, want the legacy pick to stick", s.EndpointID(), s.Model())
	}
	if note := s.StartupNote(); note != "" {
		t.Fatalf("StartupNote = %q, want no note for a backfill", note)
	}
	sv := endpoint.LoadSaved(s.savedPath)
	if sv.ConfigFingerprint == "" || sv.ConfigFingerprint != s.endpoints.Fingerprint(sv) {
		t.Fatalf("Saved = %+v, want the current fingerprint backfilled", sv)
	}

	// Now that it is fingerprinted, a later edit is detected.
	edited := cfg
	edited.BaseURL = "http://localhost:8080/v1"
	next := newTestSessionWithConfig(t, edited)
	next.restoreStartupEndpoint()
	if next.EndpointID() != ConfigProviderID || next.StartupNote() == "" {
		t.Fatalf("after an edit: on %q note=%q, want config.yaml with a note", next.EndpointID(), next.StartupNote())
	}
}

func TestIgnoreSavedEndpointUsesTheConfigAndLeavesProviderJSON(t *testing.T) {
	cfg := regoloPickConfig(t)
	pickRegolo(t, cfg)

	for name, edit := range map[string]func(*types.Config){
		"unchanged config": func(*types.Config) {},
		"edited config":    func(c *types.Config) { c.BaseURL = "http://localhost:8080/v1" },
	} {
		t.Run(name, func(t *testing.T) {
			c := cfg
			edit(&c)
			c.IgnoreSavedEndpoint = true
			s := newTestSessionWithConfig(t, c)
			before := readFile(t, s.savedPath)
			s.restoreStartupEndpoint()
			if s.EndpointID() != ConfigProviderID || s.Model() != "uncensored" {
				t.Fatalf("started on %q/%q, want config.yaml", s.EndpointID(), s.Model())
			}
			if note := s.StartupNote(); note != "" {
				t.Fatalf("StartupNote = %q, want none", note)
			}
			if after := readFile(t, s.savedPath); after != before || before == "" {
				t.Fatalf("provider.json changed or missing:\n%s\n->\n%s", before, after)
			}
		})
	}
}

func TestBareSwitchToTheDefaultThenConfigEditHasNoNote(t *testing.T) {
	cfg := types.Config{BaseDir: t.TempDir(), Model: "old-model", BaseURL: "http://localhost:8080/v1"}
	first := newTestSessionWithConfig(t, cfg)
	if err := first.SwitchProvider(ConfigProviderID, ""); err != nil {
		t.Fatal(err)
	}
	edited := cfg
	edited.BaseURL = "http://localhost:9090/v1"
	second := newTestSessionWithConfig(t, edited)
	second.restoreStartupEndpoint()
	if note := second.StartupNote(); note != "" {
		t.Fatalf("StartupNote = %q, want none: a bare default pick overrides nothing", note)
	}
}
