package manage

import (
	"os"
	"testing"
)

func TestAddAndListMCPServer(t *testing.T) {
	c, _ := newTestConfigurator(t)
	if err := c.AddMCPServer("weather", "weather-mcp", []string{"--stdio"}, map[string]string{"KEY": "v"}); err != nil {
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
	_ = c.AddMCPServer("a", "cmd-a", nil, nil)
	if err := c.RemoveMCPServer("a"); err != nil {
		t.Fatalf("RemoveMCPServer: %v", err)
	}
	if err := c.RemoveMCPServer("a"); err == nil {
		t.Fatalf("expected error removing unknown server")
	}
}

func TestWriterPreservesUnknownKeys(t *testing.T) {
	c, _ := newTestConfigurator(t)
	if err := os.WriteFile(c.configPath, []byte("model: gpt-test\nlog_level: debug\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if err := c.AddMCPServer("a", "cmd-a", nil, nil); err != nil {
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
