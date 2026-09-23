package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/nib/types"
)

func settingKeys() map[string]Setting {
	out := map[string]Setting{}
	for _, s := range Settings() {
		out[s.Key] = s
	}
	return out
}

// The key list is reflected from types.Config's yaml tags, so it follows the
// struct: nested scalars appear as dotted paths, containers never do, and
// runtime-only (yaml:"-") fields stay out of reach.
func TestSettingsReflectsScalarKeys(t *testing.T) {
	keys := settingKeys()
	for key, typ := range map[string]SettingType{
		"compaction.threshold":                  SettingFloat,
		"compaction.disabled":                   SettingBool,
		"tool_output_pruning.high_water_tokens": SettingInt,
		"ui.hide_hud":                           SettingBool,
		"approval_mode":                         SettingString,
		"model":                                 SettingString,
		"agent_options.iterations":              SettingInt,
		"session_retention":                     SettingInt,
		"browser.enabled":                       SettingBool,
	} {
		s, ok := keys[key]
		if !ok {
			t.Errorf("missing settable key %q", key)
			continue
		}
		if s.Type != typ {
			t.Errorf("%s: type %q, want %q", key, s.Type, typ)
		}
	}
	for _, key := range []string{
		"mcp_servers", "agents", "hooks", "skills", "commands", "allowed_tools",
		"metadata", "prompt_fragments", "codex_app_server.args",
		// runtime-only
		"trace_dir", "base_dir", "program_name", "browser.session_id",
	} {
		if _, ok := keys[key]; ok {
			t.Errorf("key %q must not be settable", key)
		}
	}
}

// Secrets are never listed: /settings echoes its input into the transcript and
// the history, and lists values back, so a key there would leak both ways.
func TestSettingsExcludeSecrets(t *testing.T) {
	for _, s := range Settings() {
		if isSecretKey(s.Key) {
			t.Errorf("secret key %q is settable", s.Key)
		}
	}
	_, err := LookupSetting("api_key")
	if err == nil || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("LookupSetting(api_key) err = %v, want a secret refusal", err)
	}
	if _, err := LookupSetting("prompt_injection_protection.classifier.api_key"); err == nil {
		t.Fatal("nested api_key must be refused too")
	}
}

func TestLookupSettingSuggestsClosestKey(t *testing.T) {
	_, err := LookupSetting("compaction.treshold")
	if err == nil || !strings.Contains(err.Error(), "compaction.threshold") {
		t.Fatalf("err = %v, want a suggestion of compaction.threshold", err)
	}
	_, err = LookupSetting("hide_hud")
	if err == nil || !strings.Contains(err.Error(), "ui.hide_hud") {
		t.Fatalf("err = %v, want a suggestion of ui.hide_hud", err)
	}
}

func TestSettingParse(t *testing.T) {
	keys := settingKeys()
	hud := keys["ui.hide_hud"]
	for raw, want := range map[string]bool{"on": true, "yes": true, "true": true, "ON": true, "off": false, "no": false, "false": false} {
		v, err := hud.Parse(raw)
		if err != nil || v != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", raw, v, err, want)
		}
	}
	if _, err := hud.Parse("maybe"); err == nil {
		t.Error("a bool must reject maybe")
	}

	iters := keys["agent_options.iterations"]
	if v, err := iters.Parse("12"); err != nil || v != 12 {
		t.Errorf("int parse = %v, %v", v, err)
	}
	if _, err := iters.Parse("1.5"); err == nil {
		t.Error("an int must reject 1.5")
	}
	if _, err := iters.Parse("-3"); err == nil {
		t.Error("an int must reject a negative count")
	}

	th := keys["compaction.threshold"]
	if v, err := th.Parse("0.7"); err != nil || v != 0.7 {
		t.Errorf("float parse = %v, %v", v, err)
	}
	if _, err := th.Parse("1.5"); err == nil {
		t.Error("threshold must reject a fraction above 1")
	}

	mode := keys["approval_mode"]
	if v, err := mode.Parse("strict"); err != nil || v != "strict" {
		t.Errorf("approval_mode parse = %v, %v", v, err)
	}
	if _, err := mode.Parse("sometimes"); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Errorf("approval_mode must reject an unknown mode and list the valid ones, got %v", err)
	}
	if got := mode.Values; strings.Join(got, ",") != "prompt,strict,allowlist,classify,auto" {
		t.Errorf("approval_mode values = %v", got)
	}
	if got := hud.Values; strings.Join(got, ",") != "on,off" {
		t.Errorf("bool values = %v", got)
	}
}

func TestSettingApplyAndFormat(t *testing.T) {
	keys := settingKeys()
	var cfg types.Config
	keys["ui.hide_hud"].Apply(&cfg, true)
	keys["compaction.threshold"].Apply(&cfg, 0.6)
	keys["compaction.keep_recent"].Apply(&cfg, 5)
	if !cfg.UI.HideHUD || cfg.Compaction.Threshold != 0.6 || cfg.Compaction.KeepRecent != 5 {
		t.Fatalf("apply did not land: %+v", cfg)
	}
	if got := keys["ui.hide_hud"].Format(cfg); got != "on" {
		t.Errorf("bool format = %q, want on", got)
	}
	if got := keys["compaction.threshold"].Format(cfg); got != "0.6" {
		t.Errorf("float format = %q", got)
	}
	if got := keys["approval_mode"].Format(cfg); got != `""` {
		t.Errorf("empty string format = %q, want quotes", got)
	}
}

const commentedConfig = `# my nib config
model: qwen3 # the model I like
unknown_future_key: keep-me
compaction:
  keep_recent: 4 # keep a few
`

func TestWriteSettingPreservesCommentsAndUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(commentedConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteSetting(path, "compaction.threshold", 0.7); err != nil {
		t.Fatal(err)
	}
	if err := WriteSetting(path, "ui.hide_hud", true); err != nil {
		t.Fatal(err)
	}
	if err := WriteSetting(path, "model", "on"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	out := string(data)
	for _, want := range []string{"# my nib config", "# keep a few", "unknown_future_key: keep-me", "keep_recent: 4", "threshold: 0.7", "hide_hud: true"} {
		if !strings.Contains(out, want) {
			t.Errorf("written file lost %q:\n%s", want, out)
		}
	}

	eff, present, err := FileSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	// "on" written as a string must read back as the string, not as a bool.
	if eff.Model != "on" || eff.Compaction.Threshold != 0.7 || !eff.UI.HideHUD || eff.Compaction.KeepRecent != 4 {
		t.Fatalf("round trip = model %q threshold %v hud %v keep %d", eff.Model, eff.Compaction.Threshold, eff.UI.HideHUD, eff.Compaction.KeepRecent)
	}
	if !present["compaction.threshold"] || !present["ui.hide_hud"] || present["compaction.reserve_tokens"] {
		t.Fatalf("present = %v", present)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v; want the original 0600 kept", info.Mode().Perm(), err)
	}
}

func TestWriteSettingCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "nib", "config.yaml")
	if err := WriteSetting(path, "ui.hide_hud", true); err != nil {
		t.Fatal(err)
	}
	eff, _, err := FileSettings(path)
	if err != nil || !eff.UI.HideHUD {
		t.Fatalf("created file: hud %v err %v", eff.UI.HideHUD, err)
	}
	// Defaults still apply to what the file leaves out.
	if eff.Compaction.Threshold != 0.8 {
		t.Fatalf("threshold = %v, want the 0.8 default", eff.Compaction.Threshold)
	}
}

// The pruning block is defaulted as a whole, so writing one of its keys into
// a file that has no block must not silently zero its siblings: the first key
// written seeds the block with the current defaults.
func TestWriteSettingSeedsBlockDefaultedPruning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := WriteSetting(path, "tool_output_pruning.disable_stale_reads", true); err != nil {
		t.Fatal(err)
	}
	eff, _, err := FileSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	p := eff.ToolOutputPruning
	if !p.DisableStaleReads || p.HighWaterTokens != 24000 || p.LowWaterTokens != 8000 || p.MinResultTokens != 200 {
		t.Fatalf("pruning = %+v, want the defaults kept beside the new key", p)
	}
}

func TestUnsetSettingRemovesKeyAndEmptyParents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(commentedConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteSetting(path, "ui.hide_hud", true); err != nil {
		t.Fatal(err)
	}
	removed, err := UnsetSetting(path, "ui.hide_hud")
	if err != nil || !removed {
		t.Fatalf("unset = %v, %v", removed, err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "ui:") || strings.Contains(string(data), "hide_hud") {
		t.Fatalf("unset left the key or an empty ui block:\n%s", data)
	}
	if !strings.Contains(string(data), "# keep a few") {
		t.Fatalf("unset lost an unrelated comment:\n%s", data)
	}
	removed, err = UnsetSetting(path, "ui.hide_hud")
	if err != nil || removed {
		t.Fatalf("second unset = %v, %v; want a no-op", removed, err)
	}
	// A missing file is not an error: there is simply nothing to unset.
	removed, err = UnsetSetting(filepath.Join(t.TempDir(), "none.yaml"), "model")
	if err != nil || removed {
		t.Fatalf("unset on a missing file = %v, %v", removed, err)
	}
}

func TestWriteSettingRefusesUnparseableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	broken := "model: [unterminated\n"
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteSetting(path, "model", "x"); err == nil {
		t.Fatal("writing into a file that does not parse must fail, not clobber it")
	}
	if data, _ := os.ReadFile(path); string(data) != broken {
		t.Fatalf("the broken file was rewritten:\n%s", data)
	}
}
