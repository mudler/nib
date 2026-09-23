package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

func classifierCfg() types.Config {
	return types.Config{
		BaseURL:   "http://127.0.0.1:1/v1",
		Endpoints: types.Endpoints{{Name: "home", ModelProviderConfig: types.ModelProviderConfig{BaseURL: "http://127.0.0.1:2/v1"}}},
	}
}

func classifierModel(t *testing.T, cfg types.Config) Model {
	t.Helper()
	m := newQueueTestModel()
	m.ctx = context.Background()
	m.cfg = cfg
	m.cfg.BaseDir = t.TempDir()
	s, err := chat.NewSession(context.Background(), m.cfg, chat.Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	m.session = s
	return m
}

func TestClassifierCommandSets(t *testing.T) {
	m := classifierModel(t, classifierCfg())
	if cmd := m.dispatchResolved("/classifier home gliner"); cmd != nil {
		t.Fatal("/classifier must not start a turn")
	}
	if info := m.session.ClassifierInfo(); info != "gliner @ home" {
		t.Fatalf("info = %q", info)
	}
	if msg := lastMessage(t, m); !strings.Contains(msg.Content, "gliner @ home") {
		t.Fatalf("notice = %q", msg.Content)
	}
}

func TestClassifierCommandDefaultEndpoint(t *testing.T) {
	m := classifierModel(t, classifierCfg())
	m.dispatchResolved("/classifier config gliner")
	if info := m.session.ClassifierInfo(); !strings.HasPrefix(info, "gliner @ ") || strings.Contains(info, "home") {
		t.Fatalf("info = %q", info)
	}
}

func TestClassifierCommandUnknownEndpoint(t *testing.T) {
	m := classifierModel(t, classifierCfg())
	m.dispatchResolved("/classifier nope gliner")
	if m.session.HasClassifier() {
		t.Fatal("classifier set on an unknown endpoint")
	}
	if msg := lastMessage(t, m); msg.Role != "error" {
		t.Fatalf("want an error, got %+v", msg)
	}
}

func TestClassifierOffLeavesClassify(t *testing.T) {
	cfg := classifierCfg()
	cfg.Classifier = types.ClassifierConfig{Model: "gliner"}
	m := classifierModel(t, cfg)
	m.dispatchResolved("/approve classify")
	m.dispatchResolved("/classifier off")
	if m.session.HasClassifier() || m.session.ApprovalMode() != "prompt" {
		t.Fatalf("classifier %v, mode %q", m.session.HasClassifier(), m.session.ApprovalMode())
	}
	if msg := lastMessage(t, m); !strings.Contains(msg.Content, "prompt") {
		t.Fatalf("notice should name the fallback: %q", msg.Content)
	}
}

func TestClassifierBareOpensEndpointPicker(t *testing.T) {
	m := classifierModel(t, classifierCfg())
	m.dispatchResolved("/classifier")
	if !m.providerPicker.active || m.providerPicker.mode != pickerClassifier {
		t.Fatal("picker not open in classifier mode")
	}
	for _, e := range m.providerPicker.all {
		if e.Kind == "provider" {
			t.Fatalf("registry provider %q offered as a classifier endpoint", e.ID)
		}
	}
	if len(m.providerPicker.all) != 2 {
		t.Fatalf("entries = %+v, want config.yaml and home", m.providerPicker.all)
	}
}

func TestClassifierPickerChainsToModelPicker(t *testing.T) {
	m := classifierModel(t, classifierCfg())
	m.dispatchResolved("/classifier")
	for i, e := range m.providerPicker.matches {
		if e.ID == "@home" {
			m.providerPicker.selected = i
		}
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if !m.modelPicker.active || !m.modelPicker.forClassifier {
		t.Fatal("model picker not open for the classifier")
	}
	// The endpoint is unreachable: the picker falls back to a typed name.
	m.modelPicker.loading = false
	m.modelPicker.typed = true
	m.modelPicker.appendQuery("gliner")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if info := m.session.ClassifierInfo(); info != "gliner @ home" {
		t.Fatalf("info = %q", info)
	}
	if m.session.Model() == "gliner" {
		t.Fatal("the classifier pick switched the chat model")
	}
}

func TestSettingsClassifierAppliesLive(t *testing.T) {
	m := classifierModel(t, classifierCfg())
	m.dispatchResolved("/settings classifier.model gliner")
	if info := m.session.ClassifierInfo(); !strings.HasPrefix(info, "gliner") {
		t.Fatalf("info = %q", info)
	}
	if msg := lastMessage(t, m); strings.Contains(msg.Content, "next start") {
		t.Fatalf("classifier setting reported as pending: %q", msg.Content)
	}
}

func TestClassifierChoiceSurvivesSessionRebuild(t *testing.T) {
	m := classifierModel(t, classifierCfg())
	m.dispatchResolved("/classifier home gliner")
	s, err := chat.NewSession(context.Background(), m.cfg, chat.Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	next, _ := m.Update(sessionReadyMsg{session: s})
	if info := next.(Model).session.ClassifierInfo(); info != "gliner @ home" {
		t.Fatalf("after rebuild info = %q", info)
	}
}

func TestClassifierDefaultEndpointNeedsModel(t *testing.T) {
	cfg := classifierCfg()
	cfg.Classifier = types.ClassifierConfig{Model: "gliner"}
	m := classifierModel(t, cfg)
	m.dispatchResolved("/classifier config")
	if !m.session.HasClassifier() {
		t.Fatal("an incomplete pick removed the classifier")
	}
	if msg := lastMessage(t, m); msg.Role != "error" {
		t.Fatalf("want an error, got %+v", msg)
	}
}
