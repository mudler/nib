package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mudler/nib/auth"
	"github.com/mudler/nib/endpoint"
	"github.com/mudler/nib/types"
)

// countingLLM answers every chat completion with a short reply and counts
// the requests it receives.
func countingLLM(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "cmpl-1", "object": "chat.completion", "created": 1, "model": "m",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "done"},
				"finish_reason": "stop",
			}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// savedRegoloPick lays out a state directory where an earlier session picked
// Regolo: a stored credential whose base_url points at trap (so a leak lands
// on a local server, never on the real provider) and provider.json holding
// the pick. raw is provider.json's content.
func savedRegoloPick(t *testing.T, trap *httptest.Server, raw string) string {
	t.Helper()
	base := t.TempDir()
	store := auth.NewStore(filepath.Join(base, "credentials.json"))
	if err := store.Save(auth.Credential{ProviderID: "regolo", Kind: auth.CredentialAPIKey, APIKey: "rg-test", BaseURL: trap.URL + "/v1"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "provider.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return base
}

func runOneTurn(t *testing.T, base, baseURL string, args ...string) {
	t.Helper()
	var out, errOut bytes.Buffer
	o := Options{
		BaseDir:     base,
		Args:        append([]string{"--cli"}, args...),
		Defaults:    types.Config{Model: "m", APIKey: "k", BaseURL: baseURL, ApprovalMode: "auto"},
		SkipSetup:   true,
		SkipBareEnv: true,
		Stdin:       strings.NewReader("hello\nexit\n"),
		Stdout:      &out,
		Stderr:      &errOut,
	}
	if code := runCtx(context.Background(), o); code != 0 {
		t.Fatalf("exit code = %d, want 0. stderr: %s", code, errOut.String())
	}
}

const legacyRegoloPick = `{"id":"regolo","model":"glm5.2"}`

// The control for the tests below: a legacy pick (no fingerprint) still
// sticks, so the trap really is where a saved pick sends requests.
func TestLegacySavedPickStillSticks(t *testing.T) {
	good, goodHits := countingLLM(t)
	trap, trapHits := countingLLM(t)
	base := savedRegoloPick(t, trap, legacyRegoloPick)
	runOneTurn(t, base, good.URL+"/v1")
	if trapHits.Load() == 0 || goodHits.Load() != 0 {
		t.Fatalf("good=%d trap=%d, want the legacy pick honored", goodHits.Load(), trapHits.Load())
	}
}

// The original leak: an explicit BaseURL was silently replaced by a pick
// saved under another config. With the fingerprint differing, the explicit
// config wins.
func TestExplicitConfigBeatsAPickSavedUnderAnotherConfig(t *testing.T) {
	good, goodHits := countingLLM(t)
	trap, trapHits := countingLLM(t)
	sv := endpoint.Saved{ID: "regolo", Model: "glm5.2"}
	sv.ConfigFingerprint = endpoint.Fingerprint(types.Config{Model: "uncensored", BaseURL: "http://elsewhere.invalid/v1"}, sv)
	data, _ := json.Marshal(sv)
	base := savedRegoloPick(t, trap, string(data))

	runOneTurn(t, base, good.URL+"/v1")
	if goodHits.Load() == 0 || trapHits.Load() != 0 {
		t.Fatalf("good=%d trap=%d, want the request on the configured BaseURL", goodHits.Load(), trapHits.Load())
	}
	if got := endpoint.LoadSaved(filepath.Join(base, "provider.json")); got.ID != "" {
		t.Fatalf("provider.json still holds %+v, want the stale pick dropped", got)
	}
}

func TestNoSavedEndpointFlagIgnoresThePick(t *testing.T) {
	good, goodHits := countingLLM(t)
	trap, trapHits := countingLLM(t)
	base := savedRegoloPick(t, trap, legacyRegoloPick)
	runOneTurn(t, base, good.URL+"/v1", "--no-saved-endpoint")
	if goodHits.Load() == 0 || trapHits.Load() != 0 {
		t.Fatalf("good=%d trap=%d, want --no-saved-endpoint to use the config", goodHits.Load(), trapHits.Load())
	}
	if data, _ := os.ReadFile(filepath.Join(base, "provider.json")); string(data) != legacyRegoloPick {
		t.Fatalf("provider.json = %s, want it untouched", data)
	}
}

func TestNoSavedEndpointEnvIgnoresThePick(t *testing.T) {
	t.Setenv("NIB_NO_SAVED_ENDPOINT", "1")
	good, goodHits := countingLLM(t)
	trap, trapHits := countingLLM(t)
	base := savedRegoloPick(t, trap, legacyRegoloPick)
	runOneTurn(t, base, good.URL+"/v1")
	if goodHits.Load() == 0 || trapHits.Load() != 0 {
		t.Fatalf("good=%d trap=%d, want NIB_NO_SAVED_ENDPOINT to use the config", goodHits.Load(), trapHits.Load())
	}
	if data, _ := os.ReadFile(filepath.Join(base, "provider.json")); string(data) != legacyRegoloPick {
		t.Fatalf("provider.json = %s, want it untouched", data)
	}
}
