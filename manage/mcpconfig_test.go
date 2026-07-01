package manage

import (
	"os"
	"testing"

	"github.com/mudler/nib/types"
)

func TestAddAndListMCPServer(t *testing.T) {
	c, _ := newTestConfigurator(t)
	if err := c.AddMCPServer("weather", types.MCPServer{Command: "weather-mcp", Args: []string{"--stdio"}, Env: map[string]string{"KEY": "v"}}); err != nil {
		t.Fatalf("AddMCPServer: %v", err)
	}
	servers, err := c.ListMCPServers()
	if err != nil {
		t.Fatalf("ListMCPServers: %v", err)
	}
	if len(servers) != 1 || servers[0].Name != "weather" || servers[0].Command != "weather-mcp" {
		t.Fatalf("unexpected servers: %+v", servers)
	}
}

func TestRemoveMCPServer(t *testing.T) {
	c, _ := newTestConfigurator(t)
	_ = c.AddMCPServer("a", types.MCPServer{Command: "cmd-a"})
	if err := c.RemoveMCPServer("a"); err != nil {
		t.Fatalf("RemoveMCPServer: %v", err)
	}
	if err := c.RemoveMCPServer("a"); err == nil {
		t.Fatalf("expected error removing unknown server")
	}
}

func TestAddMCPServerRemoteAndGet(t *testing.T) {
	c, _ := newTestConfigurator(t)
	if err := c.AddMCPServer("remote", types.MCPServer{URL: "https://example/mcp", Transport: "sse"}); err != nil {
		t.Fatalf("AddMCPServer remote: %v", err)
	}
	got, err := c.GetMCPServer("remote")
	if err != nil {
		t.Fatalf("GetMCPServer: %v", err)
	}
	if got.URL != "https://example/mcp" || got.Transport != "sse" {
		t.Fatalf("unexpected server: %+v", got)
	}
	if _, err := c.GetMCPServer("missing"); err == nil {
		t.Fatalf("expected error for missing server")
	}
}

func TestAddMCPServerValidation(t *testing.T) {
	c, _ := newTestConfigurator(t)
	if err := c.AddMCPServer("bad", types.MCPServer{}); err == nil {
		t.Fatalf("expected error: neither command nor url")
	}
	if err := c.AddMCPServer("bad", types.MCPServer{Command: "x", URL: "http://y"}); err == nil {
		t.Fatalf("expected error: both command and url")
	}
}

func TestWriterPreservesUnknownKeys(t *testing.T) {
	c, _ := newTestConfigurator(t)
	if err := os.WriteFile(c.configPath, []byte("model: gpt-test\nlog_level: debug\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if err := c.AddMCPServer("a", types.MCPServer{Command: "cmd-a"}); err != nil {
		t.Fatalf("AddMCPServer: %v", err)
	}
	data, _ := os.ReadFile(c.configPath)
	got := string(data)
	if !containsAll(got, "model: gpt-test", "log_level: debug", "mcp_servers", "cmd-a") {
		t.Fatalf("writer dropped keys:\n%s", got)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestAddMCPServerAuthValidation(t *testing.T) {
	c, _ := newTestConfigurator(t)
	// token/headers only valid for remote (url) servers
	if err := c.AddMCPServer("bad", types.MCPServer{Command: "cmd", BearerToken: "tok"}); err == nil {
		t.Fatalf("expected error: token on stdio server")
	}
	if err := c.AddMCPServer("bad", types.MCPServer{Command: "cmd", Headers: map[string]string{"X-Api-Key": "k"}}); err == nil {
		t.Fatalf("expected error: headers on stdio server")
	}
	// BearerToken + Headers["Authorization"] (any case) is ambiguous
	if err := c.AddMCPServer("bad", types.MCPServer{URL: "https://x", BearerToken: "tok", Headers: map[string]string{"Authorization": "Bearer other"}}); err == nil {
		t.Fatalf("expected error: BearerToken + Authorization header")
	}
	if err := c.AddMCPServer("bad", types.MCPServer{URL: "https://x", BearerToken: "tok", Headers: map[string]string{"authorization": "Bearer other"}}); err == nil {
		t.Fatalf("expected error: BearerToken + lowercase authorization header")
	}
	// valid combinations
	if err := c.AddMCPServer("ok1", types.MCPServer{URL: "https://x", BearerToken: "tok"}); err != nil {
		t.Fatalf("AddMCPServer token: %v", err)
	}
	if err := c.AddMCPServer("ok2", types.MCPServer{URL: "https://y", Headers: map[string]string{"X-Api-Key": "k"}}); err != nil {
		t.Fatalf("AddMCPServer headers: %v", err)
	}
	got, err := c.GetMCPServer("ok1")
	if err != nil || got.BearerToken != "tok" {
		t.Fatalf("GetMCPServer ok1: %+v, err=%v", got, err)
	}
}

func TestListMCPServersRedactsAuth(t *testing.T) {
	c, _ := newTestConfigurator(t)
	if err := c.AddMCPServer("plain", types.MCPServer{URL: "https://a"}); err != nil {
		t.Fatalf("AddMCPServer plain: %v", err)
	}
	if err := c.AddMCPServer("authed", types.MCPServer{URL: "https://b", BearerToken: "tok"}); err != nil {
		t.Fatalf("AddMCPServer authed: %v", err)
	}
	servers, err := c.ListMCPServers()
	if err != nil {
		t.Fatalf("ListMCPServers: %v", err)
	}
	byName := map[string]MCPServerInfo{}
	for _, s := range servers {
		byName[s.Name] = s
	}
	if byName["plain"].Authenticated {
		t.Fatalf("plain server should not be marked authenticated")
	}
	if !byName["authed"].Authenticated {
		t.Fatalf("authed server should be marked authenticated")
	}
}
