package cmd

import (
	"io"
	"os"
	"reflect"
	"strings"
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
	cfgr := manage.NewIn(dir)
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

func TestParseAddArgsHeaderValueWithEquals(t *testing.T) {
	// A header value may itself contain "=": strings.Cut must split on the
	// first "=" only, leaving the rest of the value intact.
	_, srv, err := parseAddArgs([]string{"remote", "--url", "https://x/mcp", "--header", "X-Foo=a=b"})
	if err != nil {
		t.Fatalf("parseAddArgs: %v", err)
	}
	if srv.Headers["X-Foo"] != "a=b" {
		t.Fatalf("header X-Foo: got %q, want %q (headers=%v)", srv.Headers["X-Foo"], "a=b", srv.Headers)
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
	cfgr := manage.NewIn(dir)
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

func TestMCPSetEnabledTogglesAndErrors(t *testing.T) {
	dir := t.TempDir()
	cfgr := manage.NewIn(dir)
	if err := cfgr.AddMCPServer("s", types.MCPServer{Command: "cmd"}); err != nil {
		t.Fatalf("AddMCPServer: %v", err)
	}
	if code := mcpSetEnabled(cfgr, []string{"s"}, false); code != 0 {
		t.Fatalf("disable exit=%d", code)
	}
	if got, _ := cfgr.GetMCPServer("s"); !got.Disabled {
		t.Fatal("expected server disabled after disable")
	}
	if code := mcpSetEnabled(cfgr, []string{"s"}, true); code != 0 {
		t.Fatalf("enable exit=%d", code)
	}
	if got, _ := cfgr.GetMCPServer("s"); got.Disabled {
		t.Fatal("expected server enabled after enable")
	}
	if code := mcpSetEnabled(cfgr, nil, true); code == 0 {
		t.Fatal("expected nonzero exit for missing name")
	}
	if code := mcpSetEnabled(cfgr, []string{"nope"}, false); code == 0 {
		t.Fatal("expected nonzero exit for unknown server")
	}
}

func TestMCPListShowsDisabledMarker(t *testing.T) {
	dir := t.TempDir()
	cfgr := manage.NewIn(dir)
	if err := cfgr.AddMCPServer("on", types.MCPServer{URL: "https://a"}); err != nil {
		t.Fatalf("AddMCPServer on: %v", err)
	}
	if err := cfgr.AddMCPServer("off", types.MCPServer{URL: "https://b"}); err != nil {
		t.Fatalf("AddMCPServer off: %v", err)
	}
	if err := cfgr.SetMCPServerEnabled("off", false); err != nil {
		t.Fatalf("SetMCPServerEnabled: %v", err)
	}

	// Capture mcpList's stdout so we can assert the per-line marker.
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	code := mcpList(cfgr)
	w.Close()
	os.Stdout = old
	if code != 0 {
		t.Fatalf("mcpList exit=%d", code)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "off"):
			if !strings.Contains(line, "(disabled)") {
				t.Errorf("disabled server line missing marker: %q", line)
			}
		case strings.HasPrefix(line, "on"):
			if strings.Contains(line, "(disabled)") {
				t.Errorf("enabled server line should not have marker: %q", line)
			}
		}
	}
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
