package lsp

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Manager owns the set of running LSP servers, one per language. It
// starts them on demand, caches the connections, and shuts them all
// down on Close.
type Manager struct {
	mu      sync.Mutex
	servers map[string]*serverEntry
	configs map[string]ServerConfig
	rootDir string
}

type serverEntry struct {
	client *Client
	err    error
}

// NewManager creates a Manager from the given server configs.
func NewManager(configs map[string]ServerConfig, rootDir string) *Manager {
	return &Manager{
		servers: make(map[string]*serverEntry),
		configs: configs,
		rootDir: rootDir,
	}
}

// langByExt maps file extensions to LSP language IDs.
var langByExt = map[string]string{
	".go":   "go",
	".ts":   "typescript",
	".tsx":  "typescriptreact",
	".js":   "javascript",
	".jsx":  "javascriptreact",
	".py":   "python",
	".rs":   "rust",
	".java": "java",
	".kt":   "kotlin",
	".rb":   "ruby",
	".c":    "c",
	".cpp":  "cpp",
	".cs":   "csharp",
}

// Server returns a ready client for the given language, starting the
// server on first call. Subsequent calls return the cached client.
func (m *Manager) Server(lang string) (*Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry, ok := m.servers[lang]; ok {
		if entry.err != nil {
			return nil, entry.err
		}
		return entry.client, nil
	}

	cfg, ok := m.configs[lang]
	if !ok {
		return nil, fmt.Errorf("no LSP server configured for language %q", lang)
	}

	client, err := Start(context.Background(), cfg, m.rootDir)
	m.servers[lang] = &serverEntry{client: client, err: err}
	if err != nil {
		return nil, err
	}
	return client, nil
}

// ServerForFile maps a file path to its language and returns the server.
func (m *Manager) ServerForFile(path string) (*Client, error) {
	ext := strings.ToLower(filepath.Ext(path))
	lang, ok := langByExt[ext]
	if !ok {
		return nil, fmt.Errorf("no LSP server for %s files", ext)
	}
	return m.Server(lang)
}

// Close shuts down all running servers.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, entry := range m.servers {
		if entry.client != nil {
			entry.client.Close()
		}
	}
	return nil
}

// Status returns a human-readable report of configured and running servers.
func (m *Manager) Status() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b strings.Builder
	if len(m.configs) == 0 {
		return "No language servers configured."
	}
	langs := make([]string, 0, len(m.configs))
	for lang := range m.configs {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	for _, lang := range langs {
		cfg := m.configs[lang]
		fullCmd := cfg.Command
		if len(cfg.Args) > 0 {
			fullCmd += " " + strings.Join(cfg.Args, " ")
		}
		state := "not running"
		if entry, ok := m.servers[lang]; ok {
			if entry.err != nil {
				state = "error: " + entry.err.Error()
			} else if entry.client != nil {
				state = "running"
			}
		}
		fmt.Fprintf(&b, "  %s: %s [%s]\n", lang, fullCmd, state)
	}
	return b.String()
}

// ConfigLines returns human-readable lines for the configured servers,
// sorted by language, each "lang: command [args...]". It does not touch state.
// Returns nil when no servers are configured.
func (m *Manager) ConfigLines() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.configs) == 0 {
		return nil
	}
	langs := make([]string, 0, len(m.configs))
	for lang := range m.configs {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	lines := make([]string, 0, len(langs))
	for _, lang := range langs {
		cfg := m.configs[lang]
		fullCmd := cfg.Command
		if len(cfg.Args) > 0 {
			fullCmd += " " + strings.Join(cfg.Args, " ")
		}
		lines = append(lines, lang+": "+fullCmd)
	}
	return lines
}
