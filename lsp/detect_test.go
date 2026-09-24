package lsp

import (
	"strings"
	"testing"
)

func TestAutoDetectFindsServers(t *testing.T) {
	detected := AutoDetect(nil)
	for lang, cfg := range detected {
		if cfg.Command == "" {
			t.Errorf("auto-detected server for %q has empty command", lang)
		}
	}
}

func TestAutoDetectSkipsExplicit(t *testing.T) {
	detected := AutoDetect(map[string]bool{"go": true})
	if _, found := detected["go"]; found {
		t.Error("AutoDetect should skip languages with explicit config")
	}
}

func TestDetectedServersFormat(t *testing.T) {
	lines := DetectedServers(nil)
	for _, line := range lines {
		if !strings.Contains(line, ": ") {
			t.Errorf("detected server line %q should contain ': '", line)
		}
	}
}

func TestDetectedServersSkipsExplicit(t *testing.T) {
	allKnown := map[string]bool{}
	for _, ks := range KnownServers {
		allKnown[ks.Lang] = true
	}
	lines := DetectedServers(allKnown)
	if len(lines) > 0 {
		t.Errorf("expected no detected servers when all are explicit, got %v", lines)
	}
}
