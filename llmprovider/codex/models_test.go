package codex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func withModelsServer(t *testing.T, body string) *LLM {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/models" || r.URL.Query().Get("client_version") == "" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("request = %s %v", r.URL, r.Header)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	old := modelsBaseURL
	modelsBaseURL = srv.URL
	t.Cleanup(func() { modelsBaseURL = old })
	return New(Config{Token: "tok"})
}

func TestListModelsSkipsHidden(t *testing.T) {
	l := withModelsServer(t, `{"models":[
		{"slug":"gpt-5-codex","visibility":"list"},
		{"slug":"gpt-5","visibility":"list"},
		{"slug":"codex-internal","visibility":"HIDE"},
		{"slug":"codex-none","visibility":"None"}
	]}`)
	ids, err := l.ListModels(context.Background())
	if err != nil || !reflect.DeepEqual(ids, []string{"gpt-5-codex", "gpt-5"}) {
		t.Fatalf("ids = %v, err = %v", ids, err)
	}
}

func TestModelLimitsPrefersContextWindow(t *testing.T) {
	l := withModelsServer(t, `{"models":[{"slug":"gpt-5.6-sol","visibility":"list","context_window":272000,"max_context_window":872000}]}`)
	limits, err := l.ModelLimits(context.Background(), "GPT-5.6-SOL")
	if err != nil {
		t.Fatal(err)
	}
	if limits.ContextWindow != 272000 || limits.ContextSource != sourceCodexModels {
		t.Fatalf("limits = %+v", limits)
	}
}

func TestModelLimitsFallsBackToMaxContextWindow(t *testing.T) {
	l := withModelsServer(t, `{"models":[{"id":"gpt-future","visibility":"list","max_context_window":872000}]}`)
	limits, err := l.ModelLimits(context.Background(), "gpt-future")
	if err != nil {
		t.Fatal(err)
	}
	if limits.ContextWindow != 872000 {
		t.Fatalf("window = %d, want 872000", limits.ContextWindow)
	}
}

func TestModelLimitsUnknownOrHidden(t *testing.T) {
	l := withModelsServer(t, `{"models":[{"slug":"secret","visibility":"hide","context_window":999999}]}`)
	for _, model := range []string{"secret", "unknown"} {
		limits, err := l.ModelLimits(context.Background(), model)
		if err != nil || limits.ContextWindow != 0 {
			t.Fatalf("model %q: limits = %+v, err = %v", model, limits, err)
		}
	}
}
