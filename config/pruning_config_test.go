package config

import (
	"testing"

	"github.com/mudler/nib/types"
)

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
