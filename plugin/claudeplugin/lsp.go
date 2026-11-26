package claudeplugin

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// LSPServer is a language server declared by a plugin, as data.
type LSPServer struct {
	Name                  string            `json:"-"`
	Command               string            `json:"command"`
	Args                  []string          `json:"args,omitempty"`
	Transport             string            `json:"transport,omitempty"`
	Env                   map[string]string `json:"env,omitempty"`
	ExtensionToLanguage   map[string]string `json:"extensionToLanguage"`
	InitializationOptions json.RawMessage   `json:"initializationOptions,omitempty"`
	Settings              json.RawMessage   `json:"settings,omitempty"`
	WorkspaceFolder       string            `json:"workspaceFolder,omitempty"`
	StartupTimeout        int               `json:"startupTimeout,omitempty"`
	ShutdownTimeout       int               `json:"shutdownTimeout,omitempty"`
	RequestTimeout        int               `json:"requestTimeout,omitempty"`
	RestartOnCrash        *bool             `json:"restartOnCrash,omitempty"`
	MaxRestarts           *int              `json:"maxRestarts,omitempty"`
	Diagnostics           *bool             `json:"diagnostics,omitempty"`
}

func parseLSPServer(name string, raw json.RawMessage) (LSPServer, error) {
	var s LSPServer
	if err := json.Unmarshal(raw, &s); err != nil {
		return LSPServer{}, err
	}
	s.Name = name
	if strings.TrimSpace(s.Command) == "" {
		return LSPServer{}, fmt.Errorf("missing command")
	}
	if len(s.ExtensionToLanguage) == 0 {
		return LSPServer{}, fmt.Errorf("missing extensionToLanguage")
	}
	return s, nil
}

func (l *loader) loadLSP(m *manifest) {
	l.p.LSPServers = map[string]LSPServer{}
	if f := filepath.Join(l.p.Root, ".lsp.json"); isFile(f) {
		l.loadLSPFile(f)
	}
	l.forEachShape("lsp", m.LSPServers, func(rel string) {
		abs, err := CheckPath(l.p.Root, rel, false)
		if err != nil {
			l.p.skip("lsp", rel, err.Error())
			return
		}
		l.loadLSPFile(abs)
	}, func(inline json.RawMessage) {
		l.addLSP(inline, "manifest")
	})
	if len(l.p.LSPServers) == 0 {
		l.p.LSPServers = nil
	}
}

func (l *loader) loadLSPFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		l.p.skip("lsp", l.rel(path), err.Error())
		return
	}
	l.addLSP(data, l.rel(path))
}

func (l *loader) addLSP(data []byte, where string) {
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(data, &servers); err != nil {
		l.p.skip("lsp", where, "invalid JSON: "+err.Error())
		return
	}
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		s, err := parseLSPServer(name, servers[name])
		if err != nil {
			l.p.skip("lsp", name, fmt.Sprintf("%s: %v", where, err))
			continue
		}
		l.p.LSPServers[name] = s
	}
}

func (l *loader) loadBin() {
	dir := filepath.Join(l.p.Root, "bin")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		l.p.Bin = append(l.p.Bin, filepath.Join(dir, e.Name()))
	}
	if len(l.p.Bin) > 0 {
		l.p.BinDir = dir
	}
}
