package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/provider"
)

// TestMain runs the package's tests against an empty home. HOME and
// XDG_CONFIG_HOME point into a temporary directory, so no test reads the
// developer's ~/.config/nib, and every provider's API key variable is unset,
// so the auth ladder cannot fall back to a key exported in the shell. A test
// that needs a config writes it under this home or passes it explicitly.
func TestMain(m *testing.M) {
	os.Exit(runIsolated(m))
}

func runIsolated(m *testing.M) int {
	home, err := os.MkdirTemp("", "nib-cmd-test-home-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(home)

	os.Setenv("HOME", home)
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for _, def := range provider.All() {
		if def.EnvVar != "" {
			os.Unsetenv(def.EnvVar)
		}
	}
	return m.Run()
}

// TestTestsDoNotSeeTheUserConfig guards the isolation TestMain sets up. A cmd
// test builds a real session, and a session starts on the provider saved in
// the user's config (provider.json, credentials.json) or on a key from the
// environment. Without isolation, a test that points its config at an
// httptest server still sent its requests to the developer's real provider,
// billed to their account, and failed whenever that provider rate-limited.
func TestTestsDoNotSeeTheUserConfig(t *testing.T) {
	tmp := filepath.Clean(os.TempDir())

	for _, v := range []string{"HOME", "XDG_CONFIG_HOME"} {
		dir := filepath.Clean(os.Getenv(v))
		if !strings.HasPrefix(dir, tmp+string(filepath.Separator)) {
			t.Errorf("%s = %q, want a directory under %s", v, dir, tmp)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || !strings.HasPrefix(filepath.Clean(home), tmp+string(filepath.Separator)) {
		t.Errorf("os.UserHomeDir() = %q, %v; want a directory under %s", home, err, tmp)
	}

	for _, def := range provider.All() {
		if def.EnvVar == "" {
			continue
		}
		if _, set := os.LookupEnv(def.EnvVar); set {
			t.Errorf("%s is set; a session could pick it up as the %s key", def.EnvVar, def.ID)
		}
	}
}
