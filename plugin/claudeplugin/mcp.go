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

// MCPServer is an MCP server declared by a plugin, as data. Nothing is
// started.
type MCPServer struct {
	Name string
	// Type is "stdio", "http", "sse" or "ws". An entry without a type is
	// "stdio" when it has a command and "http" when it has a url.
	Type    string
	Command string
	Args    []string
	Env     map[string]string
	Cwd     string

	URL           string
	Headers       map[string]string
	HeadersHelper string

	// Raw is the entry as written, for fields this struct does not model
	// (oauth, timeouts).
	Raw json.RawMessage
}

// IsRemote reports whether the server is reached over the network (http,
// sse, ws) rather than started as a local process.
func (s MCPServer) IsRemote() bool { return s.Type != "stdio" }

// Expand returns a copy with variables substituted (see [ExpandConfig]) in
// command, args, env, cwd, url, headers and headersHelper. missing lists the
// ${VAR} names that resolved to nothing.
func (s MCPServer) Expand(v Vars) (out MCPServer, missing []string) {
	out = s
	x := func(str string) string { return ExpandConfig(str, v, &missing) }
	out.Command = x(s.Command)
	out.Cwd = x(s.Cwd)
	out.URL = x(s.URL)
	out.HeadersHelper = x(s.HeadersHelper)
	if s.Args != nil {
		out.Args = make([]string, len(s.Args))
		for i, a := range s.Args {
			out.Args[i] = x(a)
		}
	}
	out.Env = expandMap(s.Env, x)
	out.Headers = expandMap(s.Headers, x)
	return out, missing
}

func expandMap(m map[string]string, x func(string) string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = x(v)
	}
	return out
}

type mcpEntry struct {
	Type          string            `json:"type"`
	Transport     string            `json:"transport"`
	Command       string            `json:"command"`
	Args          []string          `json:"args"`
	Env           map[string]string `json:"env"`
	Cwd           string            `json:"cwd"`
	URL           string            `json:"url"`
	Headers       map[string]string `json:"headers"`
	HeadersHelper string            `json:"headersHelper"`
}

// ParseMCPServer decodes one server entry.
func ParseMCPServer(name string, raw json.RawMessage) (MCPServer, error) {
	var e mcpEntry
	if err := json.Unmarshal(raw, &e); err != nil {
		return MCPServer{}, err
	}
	t := strings.ToLower(e.Type)
	if t == "" {
		t = strings.ToLower(e.Transport)
	}
	if t == "streamable-http" || t == "streamablehttp" {
		t = "http"
	}
	if t == "" {
		switch {
		case e.Command != "":
			t = "stdio"
		case e.URL != "":
			t = "http"
		}
	}
	switch t {
	case "stdio":
		if strings.TrimSpace(e.Command) == "" {
			return MCPServer{}, fmt.Errorf("stdio server needs a command")
		}
	case "http", "sse", "ws":
		if strings.TrimSpace(e.URL) == "" {
			return MCPServer{}, fmt.Errorf("%s server needs a url", t)
		}
	case "":
		return MCPServer{}, fmt.Errorf("needs a command or a url")
	default:
		return MCPServer{}, fmt.Errorf("unknown server type %q", t)
	}
	return MCPServer{
		Name: name, Type: t, Command: e.Command, Args: e.Args, Env: e.Env, Cwd: e.Cwd,
		URL: e.URL, Headers: e.Headers, HeadersHelper: e.HeadersHelper, Raw: raw,
	}, nil
}

// serverMap decodes a .mcp.json document: either {"mcpServers": {...}} or
// the bare map of name to server.
func serverMap(data []byte, wrapper string) (map[string]json.RawMessage, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, err
	}
	if inner, ok := top[wrapper]; ok {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(inner, &m); err == nil {
			return m, nil
		}
	}
	return top, nil
}

func (l *loader) loadMCP(m *manifest) {
	l.p.MCPServers = map[string]MCPServer{}
	if f := filepath.Join(l.p.Root, ".mcp.json"); isFile(f) {
		l.loadMCPFile(f)
	}
	l.forEachShape("mcp", m.MCPServers, func(rel string) {
		if strings.HasSuffix(rel, ".mcpb") || strings.HasSuffix(rel, ".dxt") {
			l.p.skip("mcp", rel, "MCP bundles (.mcpb, .dxt) are not supported")
			return
		}
		abs, err := CheckPath(l.p.Root, rel, false)
		if err != nil {
			l.p.skip("mcp", rel, err.Error())
			return
		}
		l.loadMCPFile(abs)
	}, func(inline json.RawMessage) {
		l.addMCPServers(inline, "plugin.json")
	})
	if len(l.p.MCPServers) == 0 {
		l.p.MCPServers = nil
	}
}

func (l *loader) loadMCPFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		l.p.skip("mcp", l.rel(path), err.Error())
		return
	}
	servers, err := serverMap(data, "mcpServers")
	if err != nil {
		l.p.skip("mcp", l.rel(path), "invalid JSON: "+err.Error())
		return
	}
	l.addServerMap(servers, l.rel(path))
}

func (l *loader) addMCPServers(raw json.RawMessage, where string) {
	servers, err := serverMap(raw, "mcpServers")
	if err != nil {
		l.p.skip("mcp", where, "invalid JSON: "+err.Error())
		return
	}
	l.addServerMap(servers, where)
}

func (l *loader) addServerMap(servers map[string]json.RawMessage, where string) {
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		s, err := ParseMCPServer(name, servers[name])
		if err != nil {
			l.p.skip("mcp", name, fmt.Sprintf("%s: %v", where, err))
			continue
		}
		l.p.MCPServers[name] = s
	}
}

// forEachShape walks a manifest field that takes a path, an inline object,
// or an array of either.
func (l *loader) forEachShape(component string, raw json.RawMessage, onPath func(string), onInline func(json.RawMessage)) {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	switch raw[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			onPath(s)
		}
	case '{':
		onInline(raw)
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			l.p.skip(component, "manifest", err.Error())
			return
		}
		for _, it := range items {
			l.forEachShape(component, it, onPath, onInline)
		}
	default:
		l.p.skip(component, "manifest", "unexpected value: "+string(raw))
	}
}
