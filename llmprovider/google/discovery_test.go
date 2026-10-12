package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestDiscoveryPreservesPartial(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Write([]byte(`{"models":[{"name":"models/gemini-future"},{"name":"models/gemini-future"}],"nextPageToken":"next"}`))
			return
		}
		http.Error(w, "later failure", 500)
	}))
	defer srv.Close()
	ids, err := New(Config{BaseURL: srv.URL, APIKey: "test"}).ListModels(context.Background())
	if err == nil || len(ids) != 1 || ids[0] != "gemini-future" {
		t.Fatalf("ids=%v err=%v; want deduplicated partial IDs and diagnostic", ids, err)
	}
}

func TestKnownChatModelsRefresh(t *testing.T) {
	ids := KnownChatModels()
	found := false
	for _, id := range ids {
		if id == "gemini-3.1-pro-preview" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing pinned chat hint: %v", ids)
	}
	ids[0] = "changed"
	if KnownChatModels()[0] == "changed" {
		t.Fatal("shared mutable hints")
	}
}

// Completeness belongs to each result, including consecutive calls on one adapter.
func TestDiscoveryResultMatrix(t *testing.T) {
	page := func(token string) any {
		return map[string]any{"models": []map[string]string{{"name": "models/gemini-future"}, {"name": "models/gemini-future"}}, "nextPageToken": token}
	}
	for _, tc := range []struct {
		name       string
		calls, ids int
		partial    bool
	}{
		{"success", 2, 1, false}, {"later-error", 2, 1, true},
		{"cycle", 3, 1, true}, {"cap", maxModelPages, 1, true},
		{"missing-token", 1, 1, false}, {"empty", 1, 0, false},
		{"first-error", 1, 0, true}, {"malformed", 2, 1, true},
		{"cancel", 1, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			var recovered atomic.Bool
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := int(calls.Add(1))
				if recovered.Load() {
					json.NewEncoder(w).Encode(page(""))
					return
				}
				var body any = page("next")
				switch tc.name {
				case "success":
					if n == 2 {
						body = page("")
					}
				case "later-error":
					if n == 2 {
						http.Error(w, "fixture failure", 500)
						return
					}
				case "first-error":
					http.Error(w, "fixture failure", 500)
					return
				case "malformed":
					if n == 2 {
						w.Write([]byte(`{`))
						return
					}
				case "cycle":
					if n == 2 {
						body = page("other")
					}
				case "cap":
					body = page(fmt.Sprint(n))
				case "missing-token":
					body = map[string]any{"models": []map[string]string{{"name": "models/gemini-future"}}}
				case "empty":
					body = map[string]any{}
				}
				json.NewEncoder(w).Encode(body)
			}))
			defer srv.Close()
			adapter := New(Config{BaseURL: srv.URL, APIKey: "fixture"})
			if tc.name == "cancel" {
				// Cancel after consuming the first response, before requesting page two.
				adapter.client = &http.Client{Transport: cancelAfterPage{base: http.DefaultTransport, cancel: cancel}}
			}
			result := adapter.DiscoverModels(ctx)
			if result.Partial != tc.partial || (result.Err != nil) != tc.partial || len(result.IDs) != tc.ids || int(calls.Load()) != tc.calls {
				t.Fatalf("result=%+v calls=%d want partial=%v ids=%d calls=%d", result, calls.Load(), tc.partial, tc.ids, tc.calls)
			}
			if tc.ids == 1 && result.IDs[0] != "gemini-future" {
				t.Fatalf("IDs=%v", result.IDs)
			}
			if tc.name == "cancel" && !errors.Is(result.Err, context.Canceled) {
				t.Fatalf("error=%v", result.Err)
			}
			recovered.Store(true)
			adapter.client = &http.Client{}
			next := adapter.DiscoverModels(context.Background())
			if next.Partial || next.Err != nil || len(next.IDs) != 1 {
				t.Fatalf("stale completeness on same adapter: %+v", next)
			}
			if result.Partial != tc.partial {
				t.Fatal("previous result changed")
			}
		})
	}
}

type cancelAfterPage struct {
	base   http.RoundTripper
	cancel context.CancelFunc
}

func (c cancelAfterPage) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	resp, err := c.base.RoundTrip(r)
	if err == nil {
		resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: c.cancel}
	}
	return resp, err
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error { err := c.ReadCloser.Close(); c.cancel(); return err }
