package lsp

import (
	"context"
	"fmt"
	"path/filepath"
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
