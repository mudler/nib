package lsp

import (
	"os/exec"
	"sort"
	"strings"
)

// KnownServers maps a language ID to the server binary and default args.
// AutoDetect scans PATH for these binaries in priority order — the first
// found for a language wins. Explicit Config.LSP entries always override
// auto-detected ones.
var KnownServers = []struct {
	Lang    string
	Command string
	Args    []string
}{
	{"go", "gopls", []string{"serve"}},
	{"typescript", "typescript-language-server", []string{"--stdio"}},
	{"python", "pyright-langserver", []string{"--stdio"}},
	{"python", "pylsp", []string{}},
	{"rust", "rust-analyzer", []string{}},
	{"c", "clangd", []string{}},
	{"cpp", "clangd", []string{}},
	{"java", "jdtls", []string{}},
	{"ruby", "solargraph", []string{"stdio"}},
}

// AutoDetect scans PATH for known language server binaries and returns
// configs for every language that has a server available. When multiple
// servers exist for the same language (e.g. pyright-langserver and pylsp
// for Python), the first found in KnownServers order wins.
//
// existing is the set of languages already explicitly configured — those
// are skipped so explicit config always takes priority.
func AutoDetect(existing map[string]bool) map[string]ServerConfig {
	detected := make(map[string]ServerConfig)

	for _, ks := range KnownServers {
		if existing[ks.Lang] {
			continue // explicit config wins
		}
		if _, already := detected[ks.Lang]; already {
			continue // first found wins
		}
		if _, err := exec.LookPath(ks.Command); err == nil {
			detected[ks.Lang] = ServerConfig{
				Command: ks.Command,
				Args:    ks.Args,
			}
		}
	}

	return detected
}

// DetectedServers returns a human-readable list of auto-detected servers
// for boot messages. It scans PATH and returns lines like "go: gopls".
// Pass alreadyConfigured to skip languages with explicit config.
func DetectedServers(alreadyConfigured map[string]bool) []string {
	detected := AutoDetect(alreadyConfigured)
	if len(detected) == 0 {
		return nil
	}
	var lines []string
	for lang, cfg := range detected {
		args := ""
		if len(cfg.Args) > 0 {
			args = " " + strings.Join(cfg.Args, " ")
		}
		lines = append(lines, lang+": "+cfg.Command+args)
	}
	sort.Strings(lines)
	return lines
}
