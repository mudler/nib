package cmd

import (
	"reflect"
	"testing"

	"github.com/mudler/nib/manage"
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
		{},                                              // missing name
		{"foo"},                                         // neither command nor url
		{"foo", "--url", "http://x", "--", "cmd"},       // both url and command
		{"foo", "--transport", "ftp", "--url", "http://x"}, // bad transport
		{"foo", "--env", "noequals", "--", "cmd"},       // bad env
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
