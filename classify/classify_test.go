package classify_test

import (
	_ "github.com/mudler/nib/classify/systemone"
	"strings"
	"testing"

	"github.com/mudler/nib/classify"
	"github.com/mudler/nib/types"
)

func TestNewNotConfigured(t *testing.T) {
	c, err := classify.New(types.Config{BaseURL: "http://x/v1"})
	if err != nil || c != nil {
		t.Fatalf("New = %v, %v; want nil, nil", c, err)
	}
}

func TestNewUnknownEndpoint(t *testing.T) {
	_, err := classify.New(types.Config{Classifier: types.ClassifierConfig{Endpoint: "nope", Model: "g"}})
	if err == nil {
		t.Fatal("want an error for an unknown endpoint")
	}
}

func TestNewUnknownAPI(t *testing.T) {
	_, err := classify.New(types.Config{BaseURL: "http://x/v1", Classifier: types.ClassifierConfig{Model: "g", API: "other"}})
	if err == nil {
		t.Fatal("want an error for an unknown api")
	}
}

func TestResolveUsesNamedEndpoint(t *testing.T) {
	cfg := types.Config{
		BaseURL: "http://main/v1", APIKey: "main-key",
		Endpoints:  types.Endpoints{{Name: "home", ModelProviderConfig: types.ModelProviderConfig{BaseURL: "http://nas/v1", APIKey: "nas-key", Model: "qwen"}}},
		Classifier: types.ClassifierConfig{Endpoint: "home", Model: "gliner"},
	}
	base, key, err := classify.Resolve(cfg)
	if err != nil || base != "http://nas/v1" || key != "nas-key" {
		t.Fatalf("Resolve = %q %q %v", base, key, err)
	}
}

func TestResolveFallsBackToTopLevel(t *testing.T) {
	cfg := types.Config{BaseURL: "http://main/v1", APIKey: "main-key", Classifier: types.ClassifierConfig{Model: "gliner"}}
	base, key, err := classify.Resolve(cfg)
	if err != nil || base != "http://main/v1" || key != "main-key" {
		t.Fatalf("Resolve = %q %q %v", base, key, err)
	}
}

func TestValidCategory(t *testing.T) {
	for _, c := range []string{"inspect", "build_test", "local_edit", "destructive", "network", "system"} {
		if !classify.ValidCategory(c) {
			t.Fatalf("%s should be valid", c)
		}
	}
	if classify.ValidCategory("safe") {
		t.Fatal("safe is not a category")
	}
}

func TestOAuthClassifierExplainsRequiredAPI(t *testing.T) {
	_, err := classify.New(types.Config{Provider: "openai-codex", Model: "chat-model", Classifier: types.ClassifierConfig{Model: "classifier-model"}})
	if err == nil || !strings.Contains(err.Error(), "SystemOne") || !strings.Contains(err.Error(), "/classifier <endpoint> <model>") {
		t.Fatalf("missing actionable classifier error: %v", err)
	}
}
