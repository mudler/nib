package codex

import (
	"context"
	"github.com/mudler/nib/provider"
	"github.com/mudler/nib/types"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
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
	if err != nil || !reflect.DeepEqual(ids, []string{"gpt-5-codex", "gpt-5", "gpt-6-astra", "gpt-6.1-sol", "gpt-6-luna"}) {
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

func TestListModelsFutureExactAndEmpty(t *testing.T) {
	for _, tc := range []struct {
		body string
		want []string
	}{
		{`{"models":[{"slug":"gpt-6-future.Exact","id":"not-the-slug","visibility":"list"},{"id":"future/other"},{"slug":"hidden","visibility":"hide"},{"slug":"none","visibility":"none"}]}`, []string{"gpt-6-future.Exact", "future/other", "gpt-6-astra", "gpt-6.1-sol", "gpt-6-luna"}},
		{`{"models":[]}`, []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6-luna"}},
	} {
		t.Run(tc.body, func(t *testing.T) {
			ids, err := withModelsServer(t, tc.body).ListModels(context.Background())
			if err != nil || !reflect.DeepEqual(ids, tc.want) {
				t.Fatalf("ids=%v err=%v", ids, err)
			}
		})
	}
}

func TestListModelsErrors(t *testing.T) {
	for _, status := range []int{200, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); w.Write([]byte(`invalid json`)) }))
			defer srv.Close()
			old := modelsBaseURL
			modelsBaseURL = srv.URL
			defer func() { modelsBaseURL = old }()
			ids, err := New(Config{Token: "test"}).ListModels(context.Background())
			if err == nil || len(ids) != 0 {
				t.Fatalf("ids=%v err=%v", ids, err)
			}
		})
	}
}

func TestListModelsIncludesUnverifiedCatalogSuggestions(t *testing.T) {
	l := withModelsServer(t, `{"models":[{"slug":"Vendor/Future:Exact"},{"slug":"gpt-6.1-sol"},{"slug":"Vendor/Future:Exact"},{"slug":"gpt-6-luna","visibility":"hide"}]}`)
	ids, err := l.ListModels(context.Background())
	want := []string{"Vendor/Future:Exact", "gpt-6.1-sol", "gpt-6-astra"}
	if err != nil || !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids=%v err=%v; want %v", ids, err, want)
	}
	partial, ok := any(l).(interface{ ModelListIsPartial() bool })
	if !ok || !partial.ModelListIsPartial() {
		t.Fatal("catalog suggestions must not claim exhaustive account access")
	}
}

func TestCatalogSuggestionsExcludeEveryHiddenCurrentModel(t *testing.T) {
	l := withModelsServer(t, `{"models":[{"slug":"gpt-6-astra","visibility":"hide"},{"slug":"gpt-6.1-sol","visibility":"none"},{"slug":"gpt-6-luna","visibility":"HIDE"}]}`)
	ids, err := l.ListModels(context.Background())
	if err != nil || len(ids) != 0 {
		t.Fatalf("hidden models reintroduced: %v, %v", ids, err)
	}
}

func TestCatalogSuggestionsStayOnOfficialChatGPTRoute(t *testing.T) {
	def, _ := provider.Get("openai-codex")
	for _, tc := range []struct {
		name, baseURL string
		want          bool
	}{
		{"registered", "", true},
		{"explicit-official", codexBaseURL + "/", true},
		{"custom", "https://example.invalid", false},
		{"lookalike", "https://chatgpt.com.example.invalid/backend-api", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withModelsServer(t, `{"models":[{"slug":"live.Exact"}]}`)
			l, err := factory(def, types.ModelProviderConfig{APIKey: "tok", BaseURL: tc.baseURL}, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			adapter := l.(*LLM)
			if adapter.ModelListIsPartial() != tc.want {
				t.Fatalf("catalog suggestions enabled=%v, want %v", adapter.catalogSuggestions, tc.want)
			}
			ids, err := adapter.ListModels(context.Background())
			if err != nil || len(ids) == 0 || ids[0] != "live.Exact" {
				t.Fatalf("live result=%v, %v", ids, err)
			}
			if tc.want && !slices.Contains(ids, "gpt-6-astra") {
				t.Fatalf("official suggestions missing: %v", ids)
			}
			if !tc.want && len(ids) != 1 {
				t.Fatalf("custom route received catalog suggestions: %v", ids)
			}
		})
	}
}
