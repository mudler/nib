package cmd

import (
	"reflect"
	"testing"

	"github.com/mudler/nib/manage"
	"github.com/mudler/nib/types"
)

func TestParseAddArgsStdio(t *testing.T) {
	name, srv, err := parseAddArgs([]string{"weather", "--env", "A=1", "--", "weather-mcp", "--stdio", "-x"})
	if err != nil {
		t.Fatalf("parseAddArgs: %v", err)
	}
	if name != "weather" || srv.Command != "weather-mcp" {
		t.Fatalf("got name=%q cmd=%q", name, srv.Command)
	}
	if !reflect.DeepEqual(srv.Args, []string{"--stdio", "-x"}) {
		t.Fatalf("args: %v", srv.Args)
	}
	if srv.Env["A"] != "1" {
		t.Fatalf("env: %v", srv.Env)
	}
}

func TestParseAddArgsRemote(t *testing.T) {
	name, srv, err := parseAddArgs([]string{"remote", "--url", "https://x/mcp", "--transport", "sse"})
	if err != nil {
		t.Fatalf("parseAddArgs: %v", err)
	}
	if name != "remote" || srv.URL != "https://x/mcp" || srv.Transport != "sse" {
		t.Fatalf("got %q %+v", name, srv)
	}
}

func TestParseAddArgsInlineEquals(t *testing.T) {
	name, srv, err := parseAddArgs([]string{"foo", "--url=https://x/mcp", "--transport=http"})
	if err != nil {
		t.Fatalf("parseAddArgs: %v", err)
	}
	if name != "foo" {
		t.Fatalf("name: got %q, want %q", name, "foo")
	}
	if srv.URL != "https://x/mcp" {
		t.Fatalf("url: got %q, want %q", srv.URL, "https://x/mcp")
	}
	if srv.Transport != "http" {
		t.Fatalf("transport: got %q, want %q", srv.Transport, "http")
	}
}

func TestParseAddArgsRepeatedEnv(t *testing.T) {
	name, srv, err := parseAddArgs([]string{"bar", "--env=A=1", "--env", "B=2", "--", "cmd"})
	if err != nil {
		t.Fatalf("parseAddArgs: %v", err)
	}
	if name != "bar" {
		t.Fatalf("name: got %q, want %q", name, "bar")
	}
	if srv.Env["A"] != "1" {
		t.Fatalf("env A: got %q, want %q (env=%v)", srv.Env["A"], "1", srv.Env)
	}
	if srv.Env["B"] != "2" {
		t.Fatalf("env B: got %q, want %q (env=%v)", srv.Env["B"], "2", srv.Env)
	}
	if srv.Command != "cmd" {
		t.Fatalf("command: got %q, want %q", srv.Command, "cmd")
	}
}

func TestParseAddArgsErrors(t *testing.T) {
	cases := [][]string{
		{},      // missing name
		{"foo"}, // neither command nor url
		{"foo", "--url", "http://x", "--", "cmd"},          // both url and command
		{"foo", "--transport", "ftp", "--url", "http://x"}, // bad transport
		{"foo", "--env", "noequals", "--", "cmd"},          // bad env
	}
	for i, args := range cases {
		if _, _, err := parseAddArgs(args); err == nil {
			t.Fatalf("case %d %v: expected error", i, args)
		}
	}
}

func TestMCPTestMissingServer(t *testing.T) {
	dir := t.TempDir()
	cfgr := manage.New(dir, dir+"/config.yaml")
	if code := mcpTest(cfgr, []string{"nope"}); code == 0 {
		t.Fatalf("expected nonzero exit for missing server")
	}
	if code := mcpTest(cfgr, nil); code == 0 {
		t.Fatalf("expected nonzero exit for missing name")
	}
}

func TestParseAddArgsToken(t *testing.T) {
	name, srv, err := parseAddArgs([]string{"remote", "--url", "https://x/mcp", "--token", "secret123"})
	if err != nil {
		t.Fatalf("parseAddArgs: %v", err)
	}
	if name != "remote" || srv.BearerToken != "secret123" {
		t.Fatalf("got name=%q token=%q", name, srv.BearerToken)
	}
}

func TestParseAddArgsTokenInlineEquals(t *testing.T) {
	_, srv, err := parseAddArgs([]string{"remote", "--url=https://x/mcp", "--token=secret123"})
	if err != nil {
		t.Fatalf("parseAddArgs: %v", err)
	}
	if srv.BearerToken != "secret123" {
		t.Fatalf("token: got %q, want %q", srv.BearerToken, "secret123")
	}
}

func TestParseAddArgsRepeatedHeader(t *testing.T) {
	_, srv, err := parseAddArgs([]string{"remote", "--url", "https://x/mcp", "--header", "X-Api-Key=k1", "--header=X-Other=v2"})
	if err != nil {
		t.Fatalf("parseAddArgs: %v", err)
	}
	if srv.Headers["X-Api-Key"] != "k1" || srv.Headers["X-Other"] != "v2" {
		t.Fatalf("headers: %v", srv.Headers)
	}
}

func TestParseAddArgsAuthErrors(t *testing.T) {
	cases := [][]string{
		{"foo", "--url", "http://x", "--token"},              // --token needs a value
		{"foo", "--url", "http://x", "--header", "noequals"}, // bad header
		{"foo", "--url", "http://x", "--header"},             // --header needs a value
	}
	for i, args := range cases {
		if _, _, err := parseAddArgs(args); err == nil {
			t.Fatalf("case %d %v: expected error", i, args)
		}
	}
}

func TestMCPListShowsAuthenticatedMarker(t *testing.T) {
	dir := t.TempDir()
	cfgr := manage.New(dir, dir+"/config.yaml")
	if err := cfgr.AddMCPServer("plain", types.MCPServer{URL: "https://a"}); err != nil {
		t.Fatalf("AddMCPServer plain: %v", err)
	}
	if err := cfgr.AddMCPServer("authed", types.MCPServer{URL: "https://b", BearerToken: "tok"}); err != nil {
		t.Fatalf("AddMCPServer authed: %v", err)
	}
	servers, err := cfgr.ListMCPServers()
	if err != nil {
		t.Fatalf("ListMCPServers: %v", err)
	}
	byName := map[string]manage.MCPServerInfo{}
	for _, s := range servers {
		byName[s.Name] = s
	}
	if byName["plain"].Authenticated {
		t.Fatalf("plain should not be authenticated")
	}
	if !byName["authed"].Authenticated {
		t.Fatalf("authed should be authenticated")
	}
	// mcpList itself writes to stdout; the redaction logic it depends on
	// (MCPServerInfo.Authenticated) is exercised above. A full stdout-capture
	// test isn't warranted here — mcpList has no existing stdout tests either,
	// consistent with the rest of this file.
}

func TestIsMCPManageSubcommand(t *testing.T) {
	for _, s := range []string{"add", "list", "remove", "test"} {
		if !IsMCPManageSubcommand(s) {
			t.Fatalf("%q should be a manage subcommand", s)
		}
	}
	for _, s := range []string{"", "--http", "--stdio", "serve"} {
		if IsMCPManageSubcommand(s) {
			t.Fatalf("%q should NOT be a manage subcommand", s)
		}
	}
}
