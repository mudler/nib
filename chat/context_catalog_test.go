package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mudler/nib/llmprovider/catalog"
	"github.com/mudler/nib/types"
)

func TestContextCatalogColdStart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, tc := range []struct {
		provider, model, url string
		want                 int
	}{
		{"anthropic", "claude-opus-4-6", "", 1000000},
		{"openai", "gpt-6.1-sol", "", 1050000},
		{"openai", "unknown", "", 128000},
		{"openai", "gpt-4.1", "http://127.0.0.1:1", 128000},
	} {
		t.Run(tc.provider+tc.model, func(t *testing.T) {
			s, err := NewSession(context.Background(), types.Config{Provider: tc.provider, Model: tc.model, BaseURL: tc.url, APIKey: "fixture", WorkingDir: t.TempDir()}, Callbacks{})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if got := s.ContextWindow(); got != tc.want {
				t.Fatalf("cold window=%d want %d", got, tc.want)
			}
		})
	}
}

func TestContextCatalogSwitch(t *testing.T) {
	s := &Session{ctx: context.Background(), compactionAutoDetected: true}
	for _, tc := range []struct {
		provider, model, url string
		want                 int
	}{
		{"openai", "gpt-6.1-sol", "", 1050000},
		{"anthropic", "claude-sonnet-4-6", "", 1000000},
		{"anthropic", "claude-sonnet-4-6", "http://127.0.0.1:1", 128000},
		{"openai", "unknown", "", 128000},
	} {
		s.learnedWindow, s.learnedWindowModel = 8192, tc.model
		s.limitsFor = tc.model
		if err := s.applyProvider(types.ModelProviderConfig{Provider: tc.provider, Model: tc.model, BaseURL: tc.url, APIKey: "fixture"}, ""); err != nil {
			t.Fatal(err)
		}
		if got := s.ContextWindow(); got != tc.want {
			t.Errorf("%s/%s: window=%d want %d", tc.provider, tc.model, got, tc.want)
		}
		if s.limitsFor != "" {
			t.Error("limits not reset")
		}
	}
}

type contextFixtureLimits struct{ window int }

func (f contextFixtureLimits) ModelLimits(context.Context, string) (catalog.Limits, error) {
	return catalog.Limits{ContextWindow: f.window}, nil
}

func TestContextCatalogLiveAndExplicit(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		s := &Session{llmModel: "gpt-6.1-sol", mainProvider: types.ModelProviderConfig{Provider: "openai-codex", Model: "gpt-6.1-sol"}, compactionAutoDetected: !explicit, modelLimitsProvider: contextFixtureLimits{272000}}
		s.compaction.MaxContextTokens = 64000
		s.ensureModelLimits(context.Background())
		want := 272000
		if explicit {
			want = 64000
		}
		if got := s.ContextWindow(); got != want {
			t.Fatalf("explicit=%v got=%d want=%d", explicit, got, want)
		}
	}
}

func TestContextCatalogCustomIsolation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	defer srv.Close()
	for _, p := range []string{"openai", "azure-openai"} {
		s := &Session{llmModel: "gpt-4.1", mainProvider: types.ModelProviderConfig{Provider: p, Model: "gpt-4.1", BaseURL: srv.URL}, compactionAutoDetected: true}
		s.compaction.MaxContextTokens = defaultContextTokens
		s.ensureModelLimits(context.Background())
		if got := s.ContextWindow(); got != defaultContextTokens {
			t.Errorf("%s inherited vendor context: %d", p, got)
		}
	}
}

func TestContextCatalogRouteFallback(t *testing.T) {
	for _, tc := range []struct {
		provider string
		want     int
	}{{"openai", 1050000}, {"openai-codex", 500000}, {"azure-openai", 128000}} {
		if got := fallbackContextSize(types.ModelProviderConfig{Provider: tc.provider, Model: "gpt-6.1-sol"}); got != tc.want {
			t.Errorf("%s got %d want %d", tc.provider, got, tc.want)
		}
	}
}

func TestContextCatalogExplicitSwitch(t *testing.T) {
	s := &Session{ctx: context.Background()}
	s.compaction.MaxContextTokens = 64000
	if err := s.applyProvider(types.ModelProviderConfig{Provider: "anthropic", Model: "claude-opus-4-6", APIKey: "fixture"}, ""); err != nil {
		t.Fatal(err)
	}
	if got := s.ContextWindow(); got != 64000 {
		t.Fatalf("explicit window replaced: %d", got)
	}
}
