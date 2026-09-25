package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/nib/types"
)

func TestReserveTokensDefaults(t *testing.T) {
	cfg := withDefaults(types.Config{})
	if cfg.Compaction.ReserveTokens != 4096 {
		t.Fatalf("ReserveTokens = %d, want 4096", cfg.Compaction.ReserveTokens)
	}
}

func TestReserveTokensExplicitValueSurvives(t *testing.T) {
	cfg := withDefaults(types.Config{
		Compaction: types.CompactionConfig{ReserveTokens: 512},
	})
	if cfg.Compaction.ReserveTokens != 512 {
		t.Fatalf("ReserveTokens = %d, want 512 preserved", cfg.Compaction.ReserveTokens)
	}
}

// A yaml tag typo is invisible to Go and to any test that calls withDefaults
// directly — the field would silently stay zero and the user's config would be
// ignored. Bind it through the real load path.
func TestReserveTokensBindsThroughTheRealLoadPath(t *testing.T) {
	dir := t.TempDir()
	body := "compaction:\n  reserve_tokens: 1234\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := LoadWith(LoadOptions{BaseDir: dir, SkipBareEnv: true})
	if cfg.Compaction.ReserveTokens != 1234 {
		t.Fatalf("ReserveTokens = %d, want 1234: 'reserve_tokens' did not bind", cfg.Compaction.ReserveTokens)
	}
}

func TestSummaryMaxTokensDefaults(t *testing.T) {
	cfg := withDefaults(types.Config{})
	if cfg.Compaction.SummaryMaxTokens != 16384 {
		t.Fatalf("SummaryMaxTokens = %d, want 16384", cfg.Compaction.SummaryMaxTokens)
	}
	cfg = withDefaults(types.Config{Compaction: types.CompactionConfig{SummaryMaxTokens: 2048}})
	if cfg.Compaction.SummaryMaxTokens != 2048 {
		t.Fatalf("SummaryMaxTokens = %d, want 2048 preserved", cfg.Compaction.SummaryMaxTokens)
	}
}

func TestSummaryMaxTokensBindsThroughTheRealLoadPath(t *testing.T) {
	dir := t.TempDir()
	body := "compaction:\n  summary_max_tokens: 3000\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := LoadWith(LoadOptions{BaseDir: dir, SkipBareEnv: true})
	if cfg.Compaction.SummaryMaxTokens != 3000 {
		t.Fatalf("SummaryMaxTokens = %d, want 3000: 'summary_max_tokens' did not bind", cfg.Compaction.SummaryMaxTokens)
	}
}
