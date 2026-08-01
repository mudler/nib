package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/nib/types"
)

func TestWithDefaultsCompaction(t *testing.T) {
	cfg := withDefaults(types.Config{})
	if cfg.Compaction.MaxContextTokens != 128000 {
		t.Fatalf("MaxContextTokens default = %d, want 128000", cfg.Compaction.MaxContextTokens)
	}
	if cfg.Compaction.Threshold != 0.8 {
		t.Fatalf("Threshold default = %v, want 0.8", cfg.Compaction.Threshold)
	}
	if cfg.Compaction.KeepRecent != 8 {
		t.Fatalf("KeepRecent default = %d, want 8", cfg.Compaction.KeepRecent)
	}
	if cfg.Compaction.Disabled {
		t.Fatal("auto-compaction should be enabled (Disabled=false) by default")
	}
}

func TestWithDefaultsKeepsUserValues(t *testing.T) {
	in := types.Config{Compaction: types.CompactionConfig{
		MaxContextTokens: 200000, Threshold: 0.5, KeepRecent: 2, Disabled: true,
	}}
	got := withDefaults(in)
	if got.Compaction != in.Compaction {
		t.Fatalf("withDefaults overrode user values: %+v", got.Compaction)
	}
}

// clearBareEnv neutralizes the MODEL/API_KEY/BASE_URL overrides LoadWith reads
// from the process environment. Without it these tests assert against whatever
// the developer happens to export: `MODEL=oops go test ./config/` fails them.
func clearBareEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MODEL", "")
	t.Setenv("API_KEY", "")
	t.Setenv("BASE_URL", "")
}

func TestLoadWithBaseDirReadsInjectedRoot(t *testing.T) {
	clearBareEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("model: injected-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := LoadWith(LoadOptions{BaseDir: dir})
	if cfg.Model != "injected-model" {
		t.Fatalf("Model = %q, want injected-model", cfg.Model)
	}
	if cfg.BaseDir != dir {
		t.Fatalf("BaseDir = %q, want %q", cfg.BaseDir, dir)
	}
}

func TestLoadWithEmptyBaseDirUsesDefaultPaths(t *testing.T) {
	clearBareEnv(t)
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "nib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "nib", "config.yaml"), []byte("model: xdg-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := LoadWith(LoadOptions{})
	if cfg.Model != "xdg-model" {
		t.Fatalf("Model = %q, want xdg-model", cfg.Model)
	}
}

// The standalone binary must keep writing self-configuration to the first
// existing config file (WritablePath), not to <base>/config.yaml. Load
// therefore leaves BaseDir empty when no root was injected, so every consumer
// that resolves it through WritablePathIn/BaseDirIn gets today's behavior.
func TestLoadWithEmptyBaseDirKeepsWritablePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	if err := os.WriteFile(filepath.Join(home, ".nib.yaml"), []byte("model: home-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := LoadWith(LoadOptions{})
	if got, want := WritablePathIn(cfg.BaseDir), filepath.Join(home, ".nib.yaml"); got != want {
		t.Fatalf("WritablePathIn(cfg.BaseDir) = %q, want %q", got, want)
	}
}

// BaseDirOf must return the injected root itself, not a path derived from it,
// and must not consult the XDG/home resolution at all when an override is set.
func TestBaseDirOfReturnsInjectedRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	dir := t.TempDir()
	if got := BaseDirOf(types.Config{BaseDir: dir}); got != dir {
		t.Fatalf("BaseDirOf = %q, want %q", got, dir)
	}
}

// With no override the field is empty, and BaseDirOf must reproduce standalone
// nib's default resolution rather than returning the empty string, which would
// make every filepath.Join on it relative to the working directory.
func TestBaseDirOfEmptyResolvesDefaultRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	want := filepath.Join(home, "nib")
	if got := BaseDirOf(types.Config{}); got != want {
		t.Fatalf("BaseDirOf = %q, want %q", got, want)
	}
}

// SkipBareEnv is what lets an embedder publish its own prefixed variables: a
// bare MODEL exported for some unrelated tool must not retarget the agent.
func TestSkipBareEnvIgnoresBareVars(t *testing.T) {
	t.Setenv("MODEL", "from-env")
	t.Setenv("API_KEY", "key-from-env")
	t.Setenv("BASE_URL", "http://env.invalid/v1")
	dir := t.TempDir()

	cfg := LoadWith(LoadOptions{BaseDir: dir, SkipBareEnv: true})
	if cfg.Model == "from-env" {
		t.Fatal("SkipBareEnv did not suppress MODEL")
	}
	if cfg.APIKey == "key-from-env" {
		t.Fatal("SkipBareEnv did not suppress API_KEY")
	}
	if cfg.BaseURL == "http://env.invalid/v1" {
		t.Fatal("SkipBareEnv did not suppress BASE_URL")
	}
}

// The zero value has to reproduce standalone nib exactly, so with the switch
// off all three bare variables still apply.
func TestBareEnvStillAppliesByDefault(t *testing.T) {
	t.Setenv("MODEL", "from-env")
	t.Setenv("API_KEY", "key-from-env")
	t.Setenv("BASE_URL", "http://env.invalid/v1")
	dir := t.TempDir()

	cfg := LoadWith(LoadOptions{BaseDir: dir})
	if cfg.Model != "from-env" {
		t.Fatalf("Model = %q, want from-env (standalone nib must be unaffected)", cfg.Model)
	}
	if cfg.APIKey != "key-from-env" {
		t.Fatalf("APIKey = %q, want key-from-env", cfg.APIKey)
	}
	if cfg.BaseURL != "http://env.invalid/v1" {
		t.Fatalf("BaseURL = %q, want the env value", cfg.BaseURL)
	}
}

// Seeds fill the gaps the config file leaves; they never override it.
func TestDefaultsSitBeneathTheConfigFile(t *testing.T) {
	clearBareEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("model: from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	defaults := types.Config{Model: "from-defaults", BaseURL: "http://seed.invalid/v1"}

	cfg := LoadWith(LoadOptions{BaseDir: dir, Defaults: defaults, SkipBareEnv: true})
	if cfg.Model != "from-file" {
		t.Fatalf("Model = %q, want from-file (the file must win over Defaults)", cfg.Model)
	}
	if cfg.BaseURL != "http://seed.invalid/v1" {
		t.Fatalf("BaseURL = %q, want the seeded value to fill the gap", cfg.BaseURL)
	}
}

// The whole ladder in one place: built-in defaults < Defaults < config file <
// environment. Each field below is set at exactly one rung above the seeds, so
// a collapsed precedence shows up as a concrete wrong value rather than as a
// missing assertion.
func TestPrecedenceDefaultsThenFileThenEnv(t *testing.T) {
	t.Setenv("MODEL", "from-env")
	t.Setenv("API_KEY", "")
	t.Setenv("BASE_URL", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"),
		[]byte("model: from-file\napi_key: key-from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	defaults := types.Config{
		Model:        "model-from-defaults",
		APIKey:       "key-from-defaults",
		BaseURL:      "http://seed.invalid/v1",
		ApprovalMode: "auto",
		Prompt:       "seeded prompt",
	}

	cfg := LoadWith(LoadOptions{BaseDir: dir, Defaults: defaults})

	// env beats file beats seeds
	if cfg.Model != "from-env" {
		t.Fatalf("Model = %q, want from-env", cfg.Model)
	}
	// file beats seeds, and the env var is unset so it does not intervene
	if cfg.APIKey != "key-from-file" {
		t.Fatalf("APIKey = %q, want key-from-file", cfg.APIKey)
	}
	// nothing above the seeds sets these, so the seeds stand
	if cfg.BaseURL != "http://seed.invalid/v1" {
		t.Fatalf("BaseURL = %q, want the seeded value", cfg.BaseURL)
	}
	if cfg.ApprovalMode != "auto" {
		t.Fatalf("ApprovalMode = %q, want auto (seeded)", cfg.ApprovalMode)
	}
	// Prompt is not among the seeded fields, so nib's built-in default stands.
	if cfg.Prompt != defaultPrompt {
		t.Fatalf("Prompt = %q, want nib's built-in default", cfg.Prompt)
	}
}

// TraceDir is runtime-only (yaml:"-"), so a seed is the only way an embedder
// can preset it, and nothing in the file can shadow it.
func TestDefaultsSeedTraceDir(t *testing.T) {
	clearBareEnv(t)
	dir := t.TempDir()
	cfg := LoadWith(LoadOptions{BaseDir: dir, Defaults: types.Config{TraceDir: "/seeded/traces"}})
	if cfg.TraceDir != "/seeded/traces" {
		t.Fatalf("TraceDir = %q, want /seeded/traces", cfg.TraceDir)
	}
}

// The zero Defaults must change nothing: an unseeded load still yields empty
// credentials, not some accidental placeholder.
func TestZeroDefaultsSeedNothing(t *testing.T) {
	clearBareEnv(t)
	dir := t.TempDir()
	cfg := LoadWith(LoadOptions{BaseDir: dir})
	if cfg.Model != "" || cfg.APIKey != "" || cfg.BaseURL != "" || cfg.ApprovalMode != "" || cfg.TraceDir != "" {
		t.Fatalf("zero Defaults seeded something: %+v", cfg)
	}
}

func TestWritablePathInPrefersInjectedRoot(t *testing.T) {
	dir := t.TempDir()
	got := WritablePathIn(dir)
	want := filepath.Join(dir, "config.yaml")
	if got != want {
		t.Fatalf("WritablePathIn = %q, want %q", got, want)
	}
}
