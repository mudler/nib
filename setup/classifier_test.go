package setup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/mudler/nib/types"
)

func TestFindClassifierModel(t *testing.T) {
	if got := FindClassifierModel([]string{"qwen3", "GLiNER2.5-large", "gliner-small"}); got != "GLiNER2.5-large" {
		t.Fatalf("got %q", got)
	}
	if got := FindClassifierModel([]string{"qwen3"}); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestProbeModelsListsIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"qwen3"},{"id":"gliner2.5"}]}`))
	}))
	defer srv.Close()
	ids, err := ProbeModels(context.Background(), "", srv.URL+"/v1")
	if err != nil || len(ids) != 2 || ids[1] != "gliner2.5" {
		t.Fatalf("ids = %v, err = %v", ids, err)
	}
}

func probedModel(t *testing.T, models ...string) model {
	t.Helper()
	m := newModel(context.Background(), types.Config{})
	m.step = stepProbe
	m.probing = true
	mi, _ := m.Update(probeResultMsg{models: models})
	return mi.(model)
}

func TestWizardOffersClassifier(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := probedModel(t, "qwen3", "gliner2.5")
	if m.classifierModel != "gliner2.5" {
		t.Fatalf("offer = %q", m.classifierModel)
	}
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = mi.(model)
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mi.(model)
	if !m.saved || m.cfg.Classifier.Model != "gliner2.5" || m.cfg.ApprovalMode != "" {
		t.Fatalf("saved = %v, classifier = %q, mode = %q", m.saved, m.cfg.Classifier.Model, m.cfg.ApprovalMode)
	}
	data, _ := os.ReadFile(m.savedPath)
	var got map[string]any
	_ = yaml.Unmarshal(data, &got)
	cl, _ := got["classifier"].(map[string]any)
	if cl["model"] != "gliner2.5" {
		t.Fatalf("written config = %v", got)
	}
	if _, ok := got["approval_mode"]; ok {
		t.Fatal("the wizard must not set approval_mode")
	}
}

func TestWizardClassifierDeclined(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := probedModel(t, "gliner2.5")
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mi.(model)
	if m.cfg.Classifier.Model != "" {
		t.Fatalf("classifier set without consent: %q", m.cfg.Classifier.Model)
	}
	data, _ := os.ReadFile(m.savedPath)
	var got map[string]any
	_ = yaml.Unmarshal(data, &got)
	if _, ok := got["classifier"]; ok {
		t.Fatalf("classifier written without consent: %v", got)
	}
}

func TestWizardNoOfferWithoutGliner(t *testing.T) {
	m := probedModel(t, "qwen3")
	if m.classifierModel != "" {
		t.Fatalf("offer = %q", m.classifierModel)
	}
}
