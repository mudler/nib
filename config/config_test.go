package config

import (
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
