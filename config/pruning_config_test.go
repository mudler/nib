package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/nib/types"
)

// loadPruningYAML drops a config.yaml carrying only a tool_output_pruning block
// into a temp root and loads it the way a real run does.
func loadPruningYAML(t *testing.T, body string) types.ToolOutputPruningConfig {
	t.Helper()
	clearBareEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadWith(LoadOptions{BaseDir: dir, SkipBareEnv: true}).ToolOutputPruning
}

// The yaml tags are load-bearing and invisible to the compiler: a typo in one
// leaves the field at its zero value and the user's setting is silently ignored,
// with no error anywhere. Nothing that calls withDefaults directly can catch
// that, so this goes through Load's real path with all five keys set to
// distinctive non-default values.
func TestPruningKeysBindThroughTheRealLoadPath(t *testing.T) {
	p := loadPruningYAML(t, "tool_output_pruning:\n"+
		"  disabled: true\n"+
		"  disable_stale_reads: true\n"+
		"  high_water_tokens: 31337\n"+
		"  low_water_tokens: 4242\n"+
		"  min_result_tokens: 77\n")

	if !p.Disabled {
		t.Error("`disabled: true` did not bind")
	}
	if !p.DisableStaleReads {
		t.Error("`disable_stale_reads: true` did not bind")
	}
	if p.HighWaterTokens != 31337 {
		t.Errorf("HighWaterTokens = %d, want 31337: `high_water_tokens` did not bind", p.HighWaterTokens)
	}
	if p.LowWaterTokens != 4242 {
		t.Errorf("LowWaterTokens = %d, want 4242: `low_water_tokens` did not bind", p.LowWaterTokens)
	}
	if p.MinResultTokens != 77 {
		t.Errorf("MinResultTokens = %d, want 77: `min_result_tokens` did not bind", p.MinResultTokens)
	}
}

// Both halves of the block-level default, pinned together because only the pair
// tells the truth: `high_water_tokens: 0` turns size pruning off ONLY when some
// sibling key is non-zero. On its own it is byte-identical to an absent block
// and gets the full defaults back — including the 24000 the user was trying to
// remove. A reader who sees only the first case would document the wrong
// incantation.
func TestPruningZeroHighWaterNeedsANonZeroSibling(t *testing.T) {
	withSibling := loadPruningYAML(t, "tool_output_pruning:\n"+
		"  high_water_tokens: 0\n"+
		"  low_water_tokens: 8000\n")
	if withSibling.HighWaterTokens != 0 {
		t.Errorf("HighWaterTokens = %d, want 0 preserved: a non-zero sibling marks the block present",
			withSibling.HighWaterTokens)
	}

	alone := loadPruningYAML(t, "tool_output_pruning:\n  high_water_tokens: 0\n")
	if alone.HighWaterTokens != 24000 {
		t.Errorf("HighWaterTokens = %d, want the 24000 default: a lone zero is indistinguishable from an absent block",
			alone.HighWaterTokens)
	}
}

// Zero value means enabled, matching the compaction block. A user who never
// heard of this feature gets it, and `disabled: true` is the opt-out.
func TestPruningDefaultsAreEnabledWithSaneThresholds(t *testing.T) {
	cfg := withDefaults(types.Config{})

	p := cfg.ToolOutputPruning
	if p.Disabled {
		t.Fatal("pruning should default to enabled")
	}
	if p.DisableStaleReads {
		t.Fatal("the stale-read rule should default to enabled")
	}
	if p.HighWaterTokens != 24000 {
		t.Fatalf("HighWaterTokens = %d, want 24000", p.HighWaterTokens)
	}
	if p.LowWaterTokens != 8000 {
		t.Fatalf("LowWaterTokens = %d, want 8000", p.LowWaterTokens)
	}
	if p.MinResultTokens != 200 {
		t.Fatalf("MinResultTokens = %d, want 200", p.MinResultTokens)
	}
}

// An explicit value must survive defaulting, or the config knob does nothing.
func TestPruningExplicitValuesSurvive(t *testing.T) {
	cfg := withDefaults(types.Config{ToolOutputPruning: types.ToolOutputPruningConfig{
		HighWaterTokens: 50000,
		LowWaterTokens:  1000,
		MinResultTokens: 10,
	}})

	p := cfg.ToolOutputPruning
	if p.HighWaterTokens != 50000 || p.LowWaterTokens != 1000 || p.MinResultTokens != 10 {
		t.Fatalf("explicit thresholds were overwritten: %+v", p)
	}
}

// The spec makes high_water_tokens: 0 the way to turn size pruning off while
// keeping the stale-read rule. Defaulting a 0 back to 24000 would make that
// impossible, so 0 must only be defaulted when the whole block is absent.
func TestPruningZeroHighWaterIsNotOverwrittenWhenOtherFieldsAreSet(t *testing.T) {
	cfg := withDefaults(types.Config{ToolOutputPruning: types.ToolOutputPruningConfig{
		LowWaterTokens: 8000,
	}})

	if cfg.ToolOutputPruning.HighWaterTokens != 0 {
		t.Fatalf("HighWaterTokens = %d, want 0 preserved: 0 is how size pruning is disabled",
			cfg.ToolOutputPruning.HighWaterTokens)
	}
}

// Disabled: true alone must survive: it is the documented opt-out, and the
// block-level default must not treat a set-but-otherwise-zero block as absent.
func TestPruningDisabledSurvivesDefaulting(t *testing.T) {
	cfg := withDefaults(types.Config{ToolOutputPruning: types.ToolOutputPruningConfig{
		Disabled: true,
	}})

	if !cfg.ToolOutputPruning.Disabled {
		t.Fatal("Disabled: true was cleared by defaulting")
	}
}
