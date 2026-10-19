package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	wizmcp "github.com/mudler/nib/mcp"
	"github.com/mudler/nib/types"
)

func TestWebFetchFollowsSessionProviderSwitch(t *testing.T) {
	for _, during := range []bool{false, true} {
		name := "after-switch"
		if during {
			name = "during-switch"
		}
		t.Run(name, func(t *testing.T) { testWebFetchProviderSwitch(t, during) })
	}
}

func testWebFetchProviderSwitch(t *testing.T, during bool) {
	var staleCalls atomic.Int32
	stale := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		staleCalls.Add(1)
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"message":"An authentication key is required"}}`))
	}))
	defer stale.Close()
	fetchStarted := make(chan struct{})
	resumeFetch := make(chan struct{})
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if during {
			close(fetchStarted)
			select {
			case <-resumeFetch:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = w.Write([]byte("model card"))
	}))
	defer page.Close()
	active := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer active-key" {
			t.Error("extraction did not use active provider credential")
		}
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "active-model" {
			t.Errorf("model = %q, want active-model", req.Model)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"extracted"},"finish_reason":"stop"}]}`))
	}))
	defer active.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg := types.Config{Model: "old-model", BaseURL: stale.URL + "/v1", BaseDir: t.TempDir(), WorkingDir: t.TempDir()}
	transports, err := wizmcp.StartTransports(ctx, cfg, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(ctx, cfg, Callbacks{}, transports...)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.applyProvider(types.ModelProviderConfig{Model: "active-model", BaseURL: active.URL + "/v1", APIKey: "active-key"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.applyProvider(types.ModelProviderConfig{Provider: "invalid-provider", Model: "invalid-model"}, ""); err == nil {
		t.Fatal("invalid provider switch unexpectedly succeeded")
	}
	if during {
		go func() {
			select {
			case <-fetchStarted:
			case <-ctx.Done():
				return
			}
			// The extraction has already taken its snapshot. The switch must only
			// affect subsequent requests, including endpoint and credentials.
			if err := s.applyProvider(types.ModelProviderConfig{Model: "later-model", BaseURL: stale.URL + "/v1", APIKey: "later-key"}, ""); err != nil {
				t.Error(err)
			}
			close(resumeFetch)
		}()
	}
	var called bool
	for _, client := range s.clients {
		listed, err := client.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, tool := range listed.Tools {
			if tool.Name != "web_fetch" {
				continue
			}
			called = true
			res, err := client.CallTool(ctx, &sdkmcp.CallToolParams{Name: "web_fetch", Arguments: map[string]any{"url": page.URL, "prompt": "extract model card"}})
			if err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(res)
			var out struct {
				Answer string `json:"answer"`
				Error  string `json:"error"`
			}
			structured, _ := json.Marshal(res.StructuredContent)
			_ = json.Unmarshal(structured, &out)
			if out.Answer != "extracted" || out.Error != "" {
				t.Fatalf("extraction did not follow selection: %s", data)
			}
		}
	}
	if !called {
		t.Fatal("web_fetch unavailable")
	}
	if staleCalls.Load() != 0 {
		t.Fatal("extraction contacted original unauthenticated endpoint")
	}
}
