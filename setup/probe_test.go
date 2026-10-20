package setup

import (
	"context"
	"errors"
	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/types"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProbeSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer srv.Close()

	if err := Probe(context.Background(), "any-model", "sk-test", srv.URL+"/v1"); err != nil {
		t.Fatalf("Probe should succeed, got %v", err)
	}
}

func TestProbeFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	if err := Probe(context.Background(), "m", "k", srv.URL+"/v1"); err == nil {
		t.Fatal("Probe should return an error on 500")
	}
}

type probeTransport func(*http.Request) (*http.Response, error)

func (f probeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProbeConfigNativeAuthAndWizardStore(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	root := t.TempDir()
	store := auth.NewStore(filepath.Join(root, "credentials.json"))
	if err := store.Save(auth.Credential{ProviderID: "anthropic", Kind: auth.CredentialOAuth, AccessToken: "oauth-sentinel"}); err != nil {
		t.Fatal(err)
	}
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	calls := 0
	expected := "config-sentinel"
	http.DefaultTransport = probeTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "api.anthropic.com" || r.URL.Path != "/v1/models" || r.Method != "GET" {
			t.Fatalf("not native discovery: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("x-api-key") != expected || r.Header.Get("anthropic-version") == "" || r.Header.Get("Authorization") != "" || r.Header.Get("anthropic-beta") != "" {
			t.Fatal("incorrect native auth headers")
		}
		if deadline, ok := r.Context().Deadline(); !ok || time.Until(deadline) > 5*time.Second {
			t.Fatal("probe timeout missing")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"claude-test"}],"has_more":false}`))}, nil
	})
	cfg := types.ModelProviderConfig{Provider: "anthropic-api-key", BaseURL: "https://api.anthropic.com", Model: "unused", APIKey: expected}
	if err := ProbeConfig(context.Background(), cfg, store); err != nil {
		t.Fatal(err)
	}
	cfg.APIKey = ""
	if err := ProbeConfig(context.Background(), cfg, store); err == nil || !strings.Contains(err.Error(), "anthropic-api-key") {
		t.Fatalf("missing key: %v", err)
	}
	if calls != 1 {
		t.Fatal("missing key made a request")
	}
	expected = "store-sentinel"
	if err := store.Save(auth.Credential{ProviderID: "anthropic-api-key", Kind: auth.CredentialAPIKey, APIKey: expected}); err != nil {
		t.Fatal(err)
	}
	for _, useDefault := range []bool{false, true} {
		base := root
		if useDefault {
			xdg := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", xdg)
			base = filepath.Join(xdg, "nib")
			if err := auth.NewStore(filepath.Join(base, "credentials.json")).Save(auth.Credential{ProviderID: "anthropic-api-key", Kind: auth.CredentialAPIKey, APIKey: expected}); err != nil {
				t.Fatal(err)
			}
			base = ""
		}
		m := newModel(context.Background(), types.Config{BaseDir: base})
		m.applyPreset(Presets()[0])
		m.collect()
		if result := m.probeCmd()().(probeResultMsg); result.err != nil {
			t.Fatal(result.err)
		}
	}
	expected = "env-sentinel"
	t.Setenv("ANTHROPIC_API_KEY", expected)
	if err := ProbeConfig(context.Background(), cfg, nil); err != nil {
		t.Fatal(err)
	}
	cfg.BaseURL = "http://custom.invalid"
	if err := ProbeConfig(context.Background(), cfg, store); err == nil {
		t.Fatal("custom inherited ambient key")
	}
}

func TestProbeConfigNativeCustomErrorsAndCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("x-api-key") != "explicit" {
			t.Error("wrong custom discovery")
		}
		http.Error(w, `{"error":{"message":"denied","type":"authentication_error"}}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	cfg := types.ModelProviderConfig{Provider: "anthropic-api-key", BaseURL: srv.URL, APIKey: "explicit"}
	if err := ProbeConfig(context.Background(), cfg, nil); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("provider error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ProbeConfig(ctx, cfg, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
