package copilot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// Exercise completion on a fresh adapter: the picker uses a different instance.
func TestCompletionDiscoversRouting(t *testing.T) {
	for _, tt := range []struct {
		name, model, metadata, path string
		unavailable                 bool
	}{
		{"chat overrides heuristic", "gpt-5", `,"supported_endpoints":["/chat/completions"]`, "/chat/completions", false},
		{"future responses", "gpt-6-future", `,"supported_endpoints":["/responses"]`, "/responses", false},
		{"messages", "future-model", `,"supported_endpoints":["/v1/messages"]`, "/v1/messages", false},
		{"absent", "gpt-5", "", "/responses", false},
		{"empty", "gpt-5", `,"supported_endpoints":[]`, "/responses", false},
		{"unavailable", "gpt-5", "", "/responses", true},
		{"unsupported", "gpt-5", `,"supported_endpoints":["/embeddings"]`, "", false},
		{"multiple retain preference", "gpt-5", `,"supported_endpoints":["/chat/completions","/responses"]`, "/responses", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			lists, posts := 0, 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/models" {
					lists++
					if tt.unavailable {
						http.Error(w, "unavailable", 503)
						return
					}
					fmt.Fprintf(w, `{"data":[{"id":%q%s}]}`, tt.model, tt.metadata)
					return
				}
				posts++
				if r.URL.Path != tt.path {
					t.Errorf("inference path = %s, want %s", r.URL.Path, tt.path)
				}
				// A protocol failure must never trigger an inference retry.
				http.Error(w, `{"error":{"message":"inference failed"}}`, 400)
			}))
			defer s.Close()
			l := New(Config{Model: tt.model, Token: "account-a"})
			l.endpoint = s.URL
			l.resolved = true
			for i := 0; i < 2; i++ {
				_, _, err := l.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{})
				if err == nil {
					t.Fatal("expected error")
				}
				if tt.path == "" && !strings.Contains(err.Error(), "supported endpoints") {
					t.Fatalf("unclear error: %v", err)
				}
			}
			if lists != 1 {
				t.Errorf("model requests = %d, want 1", lists)
			}
			wantPosts := 2
			if tt.path == "" {
				wantPosts = 0
			}
			if posts != wantPosts {
				t.Errorf("inference requests = %d, want %d (no retries)", posts, wantPosts)
			}
		})
	}
}

func TestCapabilityCacheIdentityAndExpiry(t *testing.T) {
	lists := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lists++
		endpoint := "/responses"
		if r.Header.Get("Authorization") == "Bearer account-b" {
			endpoint = "/chat/completions"
		}
		fmt.Fprintf(w, `{"data":[{"id":"future","supported_endpoints":[%q]},{"id":"other","supported_endpoints":["/v1/messages"]}]}`, endpoint)
	}))
	defer s.Close()
	l := New(Config{Token: "account-a"})
	l.endpoint = s.URL
	l.resolved = true
	check := func(model string, want protocolRoute) {
		t.Helper()
		m, err := l.modelMetadata(context.Background(), l.endpoint, model)
		if err != nil {
			t.Fatal(err)
		}
		got, err := m.route(model)
		if err != nil || got != want {
			t.Fatalf("route %s, %v, want %s", got, err, want)
		}
	}
	if _, err := l.ListModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	check("future", routeResponses)
	check("other", routeMessages)
	if lists != 1 {
		t.Fatalf("picker cache missed: %d", lists)
	}
	l.config.Token = "account-b"
	check("future", routeChat)
	l.endpoint = s.URL + "/other-account-endpoint"
	check("future", routeChat)
	l.metadataExpires = time.Time{}
	check("future", routeChat)
	if lists != 4 {
		t.Fatalf("identity/expiry requests: %d", lists)
	}
	// A separate adapter must not inherit another account's cache.
	other := New(Config{Token: "account-a"})
	other.endpoint = s.URL
	other.resolved = true
	if _, err := other.modelMetadata(context.Background(), s.URL, "future"); err != nil {
		t.Fatal(err)
	}
	if lists != 5 {
		t.Fatalf("separate adapter requests: %d", lists)
	}
}

func TestCapabilityDiscoveryCancellation(t *testing.T) {
	started := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("retry") == "yes" {
			fmt.Fprint(w, `{"data":[]}`)
			return
		}
		close(started)
		<-r.Context().Done()
	}))
	defer s.Close()
	l := New(Config{})
	l.endpoint = s.URL
	l.resolved = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := l.CreateChatCompletion(ctx, openai.ChatCompletionRequest{Model: "gpt-5"})
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if !l.metadataExpires.IsZero() {
		t.Fatal("cancelled discovery cached")
	}
}

func TestCapabilityCacheConcurrent(t *testing.T) {
	var lists atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lists.Add(1)
		fmt.Fprint(w, `{"data":[{"id":"future","supported_endpoints":["/responses"]}]}`)
	}))
	defer s.Close()
	l := New(Config{})
	l.endpoint = s.URL
	l.resolved = true
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, err := l.modelMetadata(context.Background(), s.URL, "future")
			if err != nil || len(m.SupportedEndpoints) != 1 {
				t.Errorf("metadata: %+v, %v", m, err)
			}
		}()
	}
	wg.Wait()
	if lists.Load() != 1 {
		t.Fatalf("requests = %d", lists.Load())
	}
}
