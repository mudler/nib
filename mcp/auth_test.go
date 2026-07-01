package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mudler/nib/types"
)

func TestAuthenticatedHTTPClientNilWhenNoAuth(t *testing.T) {
	if c := authenticatedHTTPClient(types.MCPServer{URL: "https://x"}); c != nil {
		t.Fatalf("expected nil client for no auth, got %+v", c)
	}
}

func TestAuthenticatedHTTPClientSetsBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := authenticatedHTTPClient(types.MCPServer{URL: srv.URL, BearerToken: "secret123"})
	if client == nil {
		t.Fatalf("expected non-nil client")
	}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if gotAuth != "Bearer secret123" {
		t.Fatalf("Authorization header: got %q, want %q", gotAuth, "Bearer secret123")
	}
}

func TestAuthenticatedHTTPClientSetsCustomHeaders(t *testing.T) {
	var gotKey, gotOther string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Api-Key")
		gotOther = r.Header.Get("X-Other")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := authenticatedHTTPClient(types.MCPServer{
		URL:     srv.URL,
		Headers: map[string]string{"X-Api-Key": "k1", "X-Other": "v2"},
	})
	if client == nil {
		t.Fatalf("expected non-nil client")
	}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if gotKey != "k1" || gotOther != "v2" {
		t.Fatalf("headers: X-Api-Key=%q X-Other=%q", gotKey, gotOther)
	}
}

func TestAuthenticatedHTTPClientSetsTokenAndCustomHeaders(t *testing.T) {
	var gotAuth, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotKey = r.Header.Get("X-Api-Key")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := authenticatedHTTPClient(types.MCPServer{
		URL:         srv.URL,
		BearerToken: "secret123",
		Headers:     map[string]string{"X-Api-Key": "k1"},
	})
	if client == nil {
		t.Fatalf("expected non-nil client")
	}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if gotAuth != "Bearer secret123" {
		t.Fatalf("Authorization header: got %q, want %q", gotAuth, "Bearer secret123")
	}
	if gotKey != "k1" {
		t.Fatalf("X-Api-Key header: got %q, want %q", gotKey, "k1")
	}
}
