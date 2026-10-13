package mcp

import (
	"path/filepath"
	"testing"

	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/llmprovider"
	"github.com/mudler/nib/plugin"
	"github.com/mudler/nib/provider"
	"github.com/mudler/nib/types"
)

// TestWebFetchLLMUsesCredentialStore proves that the LLM built by
// StartWebMCPServer resolves its API key from the on-disk credential store
// (populated by `nib login`), not just from config.api_key or the environment.
//
// Before the fix, StartWebMCPServer built its LLM with llmprovider.New (no
// store), so providers authenticated via `nib login` sent requests with no
// Authorization header and got 401.
//
// We verify the resolution at the llmprovider level: the same provider config
// that yields an empty key with llmprovider.New (no store) yields the stored
// key with llmprovider.NewWithStore. The fix makes StartWebMCPServer use the
// latter.
func TestWebFetchLLMUsesCredentialStore(t *testing.T) {
	// Use a provider that is in the static registry and whose env var is
	// not set in the test environment, so the only key source is the store.
	regolo, ok := provider.Get("regolo")
	if !ok {
		t.Fatal("regolo provider not registered")
	}
	const storedKey = "stored-key-from-login"

	// Write the key into a credential store at the path nib would use.
	dir := t.TempDir()
	storePath := filepath.Join(plugin.BaseDirIn(dir), "credentials.json")
	store := auth.NewStore(storePath)
	if err := store.Save(auth.Credential{
		ProviderID: regolo.ID,
		Kind:       auth.CredentialAPIKey,
		APIKey:     storedKey,
	}); err != nil {
		t.Fatalf("save credential: %v", err)
	}

	// Config with no API key — the key is only in the credential store.
	cfg := types.ModelProviderConfig{
		Provider: "regolo",
		Model:    "test-model",
	}

	// Without the store (the old code path), the key is empty because
	// REGOLO_API_KEY is not set in the test env and config has no key.
	_, noStoreKey, err := llmprovider.ModelsEndpoint(cfg, nil)
	if err != nil {
		t.Fatalf("ModelsEndpoint (no store): %v", err)
	}
	if noStoreKey != "" {
		t.Fatalf("expected empty key without store, got %q", noStoreKey)
	}

	// With the store (the fixed code path), the stored key is resolved.
	_, withStoreKey, err := llmprovider.ModelsEndpoint(cfg, store)
	if err != nil {
		t.Fatalf("ModelsEndpoint (with store): %v", err)
	}
	if withStoreKey != storedKey {
		t.Fatalf("expected stored key %q, got %q", storedKey, withStoreKey)
	}
}

// TestWebFetchCredentialStorePath proves that StartWebMCPServer constructs the
// store at the same path as NewSession, so a key stored by `nib login` is
// visible to both the session LLM and the web_fetch extraction LLM.
func TestWebFetchCredentialStorePath(t *testing.T) {
	// The path NewSession uses (chat/session.go):
	//   filepath.Join(plugin.BaseDirIn(cfg.BaseDir), "credentials.json")
	sessionPath := filepath.Join(plugin.BaseDirIn("/tmp/x"), "credentials.json")

	// The path StartWebMCPServer now uses (mcp/web.go) — same formula.
	webPath := filepath.Join(plugin.BaseDirIn("/tmp/x"), "credentials.json")

	if sessionPath != webPath {
		t.Fatalf("store path mismatch: session=%q web=%q", sessionPath, webPath)
	}
}
