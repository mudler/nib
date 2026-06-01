package plugin

import "errors"

// ErrClaudeUnsupported is returned by the Claude adapter until a later phase implements it.
var ErrClaudeUnsupported = errors.New("claude code plugin format not yet supported (planned: P4)")

// loadClaudeManifest is a P0 stub. A later phase maps .claude-plugin/ (plugin.json,
// skills/, commands/, agents/, hooks.json, .mcp.json) into a Manifest.
func loadClaudeManifest(dir string) (Manifest, error) {
	return Manifest{}, ErrClaudeUnsupported
}
