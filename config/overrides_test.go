package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mudler/nib/types"
	"gopkg.in/yaml.v3"
)

// The reported bug, verbatim. LocalAI embeds nib as `local-ai chat` and passes
// --endpoint and --model down; routed through Defaults both are accepted and
// then discarded, because LocalAI seeds base_url into the config on first run
// and its model picker persists model, so from the second run on the file
// carries both. Against a live server that showed up as a probe on the flag's
// endpoint followed by every agent turn POSTing to the file's, and as a trace
// full of requests for the model the user did not ask for.
func TestOverridesBeatTheConfigFile(t *testing.T) {
	clearBareEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(
		"base_url: http://127.0.0.1:9999/v1\n"+
			"model: gemma-4-e2b-it-qat-q4_0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	overrides := types.Config{
		BaseURL: "http://127.0.0.1:8080",
		Model:   "lfm2.5-8b-a1b",
	}

	cfg := LoadWith(LoadOptions{BaseDir: dir, Overrides: overrides, SkipBareEnv: true})
	if cfg.BaseURL != "http://127.0.0.1:8080" {
		t.Fatalf("BaseURL = %q, want the --endpoint override: every turn would go to the file's 9999", cfg.BaseURL)
	}
	if cfg.Model != "lfm2.5-8b-a1b" {
		t.Fatalf("Model = %q, want the --model override", cfg.Model)
	}
}

// The whole ladder, top rung included: built-in defaults < Defaults < file <
// env < Overrides. Each field below is contested at a different pair of rungs,
// so a collapsed precedence is a concrete wrong value rather than a gap.
func TestOverridesSitAtTheTopOfThePrecedenceChain(t *testing.T) {
	t.Setenv("MODEL", "from-env")
	t.Setenv("API_KEY", "key-from-env")
	t.Setenv("BASE_URL", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(
		"model: from-file\napi_key: key-from-file\nlog_level: info\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	defaults := types.Config{
		Model:        "model-from-defaults",
		BaseURL:      "url-from-defaults",
		Prompt:       "prompt-from-defaults",
		ApprovalMode: "strict",
	}
	overrides := types.Config{
		Model:        "from-overrides",    // contested by the file AND the env
		APIKey:       "key-from-override", // contested by the file AND the env
		LogLevel:     "debug",             // contested by the file only
		BaseURL:      "url-from-override", // contested by Defaults only
		ApprovalMode: "auto",              // contested by Defaults only
		Prompt:       "prompt-from-override",
		AgentOptions: types.AgentOptions{Iterations: 42}, // contested by nib's built-in 10
	}

	cfg := LoadWith(LoadOptions{BaseDir: dir, Defaults: defaults, Overrides: overrides})

	if cfg.Model != "from-overrides" {
		t.Fatalf("Model = %q, want from-overrides: an override must beat the env, which beats the file", cfg.Model)
	}
	if cfg.APIKey != "key-from-override" {
		t.Fatalf("APIKey = %q, want key-from-override", cfg.APIKey)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q, want debug: an override must beat the file", cfg.LogLevel)
	}
	if cfg.BaseURL != "url-from-override" {
		t.Fatalf("BaseURL = %q, want url-from-override: an override must beat a seed", cfg.BaseURL)
	}
	if cfg.ApprovalMode != "auto" {
		t.Fatalf("ApprovalMode = %q, want auto", cfg.ApprovalMode)
	}
	if cfg.Prompt != "prompt-from-override" {
		t.Fatalf("Prompt = %q, want prompt-from-override", cfg.Prompt)
	}
	if cfg.AgentOptions.Iterations != 42 {
		t.Fatalf("AgentOptions.Iterations = %d, want 42: an override must beat nib's built-in", cfg.AgentOptions.Iterations)
	}
	// Untouched by the overrides, so the rungs below still resolve normally.
	if cfg.AgentOptions.MaxAttempts != 3 {
		t.Fatalf("AgentOptions.MaxAttempts = %d, want nib's built-in 3", cfg.AgentOptions.MaxAttempts)
	}
}

// The bare environment block is the rung directly beneath Overrides, and it is
// the one an embedder is most likely to be surprised by, so it is pinned on its
// own with no config file in the way.
func TestOverridesBeatTheBareEnvironment(t *testing.T) {
	t.Setenv("MODEL", "from-env")
	t.Setenv("API_KEY", "key-from-env")
	t.Setenv("BASE_URL", "http://env.invalid/v1")
	dir := t.TempDir()
	overrides := types.Config{
		Model:   "from-override",
		APIKey:  "key-from-override",
		BaseURL: "http://override.invalid/v1",
	}

	cfg := LoadWith(LoadOptions{BaseDir: dir, Overrides: overrides})
	if cfg.Model != "from-override" || cfg.APIKey != "key-from-override" ||
		cfg.BaseURL != "http://override.invalid/v1" {
		t.Fatalf("env beat the overrides: Model=%q APIKey=%q BaseURL=%q",
			cfg.Model, cfg.APIKey, cfg.BaseURL)
	}
}

// An unset Overrides must reproduce today's load exactly. The differential is
// the assertion that matters: an implementation that assigns rather than merges
// would blank every field the file set, and a struct comparison catches that
// for fields no hand-written check would think to name.
func TestUnsetOverridesChangeNothing(t *testing.T) {
	clearBareEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(
		"model: from-file\napi_key: key-from-file\nbase_url: http://file.invalid/v1\n"+
			"log_level: info\napproval_mode: strict\n"+
			"allowed_tools:\n  - from_file\n"+
			"agent_options:\n  iterations: 3\n  force_reasoning: true\n"+
			"compaction:\n  disabled: true\n  keep_recent: 4\n"+
			"metadata:\n  tenant: from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	without := LoadWith(LoadOptions{BaseDir: dir, SkipBareEnv: true})
	zero := LoadWith(LoadOptions{BaseDir: dir, Overrides: types.Config{}, SkipBareEnv: true})

	if !reflect.DeepEqual(without, zero) {
		t.Fatalf("a zero Overrides changed the load:\n without = %+v\n    zero = %+v", without, zero)
	}
	// Spelled out for the fields an assign-don't-merge bug would visibly eat.
	if zero.Model != "from-file" || zero.APIKey != "key-from-file" ||
		zero.BaseURL != "http://file.invalid/v1" || zero.ApprovalMode != "strict" {
		t.Fatalf("a zero Overrides blanked the file's scalars: %+v", zero)
	}
	if !zero.Compaction.Disabled || !zero.AgentOptions.ForceReasoning {
		t.Fatalf("a zero Overrides cleared the file's bools: %+v %+v", zero.Compaction, zero.AgentOptions)
	}
	if len(zero.AllowedTools) != 1 || zero.AllowedTools[0] != "from_file" {
		t.Fatalf("a zero Overrides discarded the file's slice: %v", zero.AllowedTools)
	}
	if zero.Metadata["tenant"] != "from-file" {
		t.Fatalf("a zero Overrides discarded the file's map: %v", zero.Metadata)
	}
}

// The documented asymmetry, pinned so it cannot regress into a surprise: an
// override can raise a bool but never lower one, because false is
// indistinguishable from unset. Browser.AllowPrivateURLs is the consequential
// one, since it gates localhost and RFC1918 access, and this says plainly that
// an embedder cannot use Overrides to take it away.
func TestOverriddenFalseCannotBeatATrueInTheFile(t *testing.T) {
	clearBareEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(
		"compaction:\n  disabled: true\n"+
			"agent_options:\n  force_reasoning: true\n"+
			"browser:\n  enabled: true\n  allow_private_urls: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	overrides := types.Config{
		Compaction:   types.CompactionConfig{Disabled: false},
		AgentOptions: types.AgentOptions{ForceReasoning: false},
		Browser:      types.BrowserConfig{Enabled: false, AllowPrivateURLs: false},
	}

	cfg := LoadWith(LoadOptions{BaseDir: dir, Overrides: overrides, SkipBareEnv: true})
	if !cfg.Compaction.Disabled || !cfg.AgentOptions.ForceReasoning ||
		!cfg.Browser.Enabled || !cfg.Browser.AllowPrivateURLs {
		t.Fatalf("an overridden false lowered a file's true; the documented caveat changed: %+v %+v %+v",
			cfg.Compaction, cfg.AgentOptions, cfg.Browser)
	}
	// The other direction does work, which is what makes the caveat one-sided
	// rather than "bools are not overridable".
	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, "config.yaml"),
		[]byte("compaction:\n  disabled: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg2 := LoadWith(LoadOptions{
		BaseDir:     dir2,
		Overrides:   types.Config{Compaction: types.CompactionConfig{Disabled: true}},
		SkipBareEnv: true,
	})
	if !cfg2.Compaction.Disabled {
		t.Fatal("an overridden true failed to beat a file's false")
	}
}

// Maps merge per key on the way up too: an overridden key wins, a key only the
// file has survives. And the merged map must be nib's own, since plugins add
// mcp_servers entries to it after the load.
func TestOverridesMergeMapsPerKeyWithoutAliasing(t *testing.T) {
	clearBareEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(
		"metadata:\n  enable_thinking: \"true\"\n  tenant: from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	callers := map[string]string{"enable_thinking": "false"}

	cfg := LoadWith(LoadOptions{
		BaseDir:     dir,
		Overrides:   types.Config{Metadata: callers},
		SkipBareEnv: true,
	})
	if got := cfg.Metadata["enable_thinking"]; got != "false" {
		t.Fatalf("Metadata[enable_thinking] = %q, want the override to win", got)
	}
	if got := cfg.Metadata["tenant"]; got != "from-file" {
		t.Fatalf("Metadata[tenant] = %q, want the file's key to survive a per-key merge", got)
	}
	cfg.Metadata["injected"] = "yes"
	if _, ok := callers["injected"]; ok {
		t.Fatal("cfg.Metadata aliases the caller's map")
	}
}

// Slices are all or nothing on the way up as well: a non-empty override
// replaces the file's list whole. The copy matters more here than for a seed,
// because mergo adopts the source slice by reference under WithOverride, and
// skill.Apply / plugin.Apply then append to cfg.Hooks, cfg.Skills and
// cfg.Commands, which would write into the embedder's own backing array.
func TestOverrideSlicesReplaceWholeAndAreNotAliased(t *testing.T) {
	clearBareEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(
		"allowed_tools:\n  - from_file\n  - also_from_file\n"+
			"hooks:\n  - event: PreToolUse\n    command: from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	backing := make([]types.HookConfig, 1, 8) // spare capacity: the dangerous shape
	backing[0] = types.HookConfig{Event: "PreToolUse", Command: "overridden"}
	overrides := types.Config{
		AllowedTools: []string{"only_this"},
		Hooks:        backing,
	}

	cfg := LoadWith(LoadOptions{BaseDir: dir, Overrides: overrides, SkipBareEnv: true})
	if len(cfg.AllowedTools) != 1 || cfg.AllowedTools[0] != "only_this" {
		t.Fatalf("AllowedTools = %v, want exactly the override's list", cfg.AllowedTools)
	}
	if len(cfg.Hooks) != 1 || cfg.Hooks[0].Command != "overridden" {
		t.Fatalf("Hooks = %+v, want exactly the override's list", cfg.Hooks)
	}
	cfg.Hooks = append(cfg.Hooks, types.HookConfig{Event: "PostToolUse", Command: "later"})
	cfg.Hooks[0].Command = "mutated"
	if backing[0].Command != "overridden" {
		t.Fatalf("cfg.Hooks aliases the caller's slice: backing[0] = %+v", backing[0])
	}
	if got := backing[:2][1].Command; got != "" {
		t.Fatalf("append wrote into the caller's spare capacity: %q", got)
	}
}

// The rot guard's other half. TestDefaultsSeedEveryFieldOfConfig proves the
// merge underneath the file carries every field; this proves the merge ABOVE it
// does, against the hardest lower layer there is: a config file with a value in
// every field it can express. Two loads over the same file, one overridden and
// one not, and every field must come back DIFFERENT.
//
// Differential for the same reason the seed guard is: withDefaults, the agent
// merge and the BaseDir assignment all write after the merge, in both loads, so
// differencing cancels them and a new one added later needs no list kept in
// sync. A field the override path cannot carry names itself here instead of
// failing silently at an embedder's call site.
//
// The two fills differ in every kind, bools included, and the file's fill is
// the FALSE one on purpose: false is the only bool value an override can beat,
// so a true-over-true file would report every bool as a phantom failure rather
// than as the documented one-way caveat that TestOverriddenFalseCannotBeatATrueInTheFile
// pins.
func TestOverrideEveryFieldOfConfig(t *testing.T) {
	clearBareEnv(t)
	dir := t.TempDir()

	var fromFile types.Config
	fillVariant{str: "from-file", b: false, i: 3, f: 0.25}.into(t, reflect.ValueOf(&fromFile).Elem())
	data, err := yaml.Marshal(fromFile)
	if err != nil {
		t.Fatalf("marshalling the fully populated config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	var overrides types.Config
	fillVariant{str: "overridden", b: true, i: 11, f: 0.75}.into(t, reflect.ValueOf(&overrides).Elem())

	overridden := LoadWith(LoadOptions{BaseDir: dir, Overrides: overrides, SkipBareEnv: true})
	plain := LoadWith(LoadOptions{BaseDir: dir, SkipBareEnv: true})

	o, p := reflect.ValueOf(overridden), reflect.ValueOf(plain)
	for i := range o.NumField() {
		name := o.Type().Field(i).Name
		differs := !reflect.DeepEqual(o.Field(i).Interface(), p.Field(i).Interface())
		if why, carved := seedCarveOuts[name]; carved {
			if differs {
				t.Errorf("types.Config.%s responded to an override, but it is a carve-out (%s)", name, why)
			}
			continue
		}
		if !differs {
			t.Errorf("types.Config.%s is identical overridden and not (%v): the override never landed",
				name, o.Field(i).Interface())
		}
	}
}

// The one carve-out applies to Overrides too. LoadOptions.BaseDir is the single
// knob for the root: honoring an override here would leave cfg claiming a root
// the config file was never read from, and an empty LoadOptions.BaseDir is the
// explicit request for standalone nib's XDG resolution rather than "unset".
func TestOverridesBaseDirIsNotOverridable(t *testing.T) {
	clearBareEnv(t)
	dir := t.TempDir()
	overrides := types.Config{BaseDir: "/overridden/root"}

	cfg := LoadWith(LoadOptions{BaseDir: dir, Overrides: overrides, SkipBareEnv: true})
	if cfg.BaseDir != dir {
		t.Fatalf("BaseDir = %q, want the LoadOptions root %q", cfg.BaseDir, dir)
	}

	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	cfg = LoadWith(LoadOptions{Overrides: overrides, SkipBareEnv: true})
	if cfg.BaseDir != "" {
		t.Fatalf("BaseDir = %q, want empty: an override must not supply the root", cfg.BaseDir)
	}
	if got, want := BaseDirOf(cfg), filepath.Join(xdg, "nib"); got != want {
		t.Fatalf("BaseDirOf = %q, want the XDG root %q", got, want)
	}
}
