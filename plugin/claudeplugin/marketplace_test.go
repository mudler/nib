package claudeplugin

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const sha40 = "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0"

func TestParseMarketplace(t *testing.T) {
	m, err := ParseMarketplace([]byte(`{
		"name": "tools", "owner": {"name": "Team"}, "metadata": {"description": "d", "version": "1.0", "pluginRoot": "./plugins"},
		"renames": {"old-fmt": "formatter", "gone": null},
		"plugins": [
			{"name": "formatter", "source": "./plugins/formatter", "tags": ["x"]},
			{"name": "bare", "source": "bare"},
			{"name": "gh", "source": {"source": "github", "repo": "org/gh", "ref": "v2", "sha": "` + sha40 + `"}},
			{"name": "gurl", "source": {"source": "url", "url": "https://git.example/r.git"}},
			{"name": "sub", "source": {"source": "git-subdir", "url": "org/mono", "path": "tools/sub"}, "strict": false},
			{"name": "pkg", "source": {"source": "npm", "package": "@org/pkg"}},
			{"name": "zip", "source": {"source": "archive", "url": "https://x/p.zip"}},
			{"name": "cmd", "source": {"source": "command", "command": "tool path"}},
			{"name": "bad sha", "source": {"source": "github", "repo": "o/r"}},
			{"name": "badsha", "source": {"source": "github", "repo": "o/r", "sha": "abc"}},
			{"name": "escape", "source": "./../x"},
			{"name": "weird", "source": {"source": "pip"}},
			{"name": "formatter", "source": "./dup"}
		]}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "tools" || m.Owner.Name != "Team" || m.Description != "d" || m.Version != "1.0" || m.PluginRoot != "./plugins" {
		t.Errorf("top = %+v", m)
	}
	if len(m.Plugins) != 8 {
		t.Errorf("plugins = %d, skipped = %+v", len(m.Plugins), m.Skipped)
	}
	if len(m.Skipped) != 5 {
		t.Errorf("skipped = %+v", m.Skipped)
	}
	supported := map[string]bool{}
	for _, e := range m.Plugins {
		supported[e.Name] = e.Source.Supported() == nil
	}
	for name, want := range map[string]bool{"formatter": true, "bare": true, "gh": true, "gurl": true, "sub": true, "pkg": false, "zip": false, "cmd": false} {
		if supported[name] != want {
			t.Errorf("%s supported = %v", name, supported[name])
		}
	}
	gh, _ := m.Find("gh")
	if gh.Source.GitURL() != "https://github.com/org/gh.git" || gh.Source.SHA != sha40 {
		t.Errorf("gh = %+v", gh.Source)
	}
	sub, _ := m.Find("sub")
	if sub.Source.GitURL() != "https://github.com/org/mono.git" || sub.Strict == nil || *sub.Strict {
		t.Errorf("sub = %+v", sub)
	}
	if e, err := m.Find("old-fmt"); err != nil || e.Name != "formatter" {
		t.Errorf("rename: %v %v", e, err)
	}
	if _, err := m.Find("gone"); err == nil || !strings.Contains(err.Error(), "removed") {
		t.Errorf("removed: %v", err)
	}
	pkg, _ := m.Find("pkg")
	if err := pkg.Source.Supported(); !errors.Is(err, ErrUnsupportedSource) {
		t.Errorf("npm: %v", err)
	}

	m.Root = t.TempDir()
	bare, _ := m.Find("bare")
	if dir, err := m.RelativeDir(bare.Source); err != nil || dir != filepath.Join(m.Root, "plugins", "bare") {
		t.Errorf("bare = %q %v", dir, err)
	}
	if _, err := m.RelativeDir(Source{Kind: SourceRelative, Path: "team/x"}); err == nil {
		t.Error("a path with '/' needs ./")
	}

	if _, err := ParseMarketplace([]byte(`{"name":"bad name","owner":{"name":"x"},"plugins":[]}`)); err == nil {
		t.Error("invalid marketplace name")
	}
}

func TestParsePluginID(t *testing.T) {
	tests := []struct {
		in       string
		name, mk string
		ok       bool
	}{
		{"fmt@tools", "fmt", "tools", true},
		{"fmt", "", "", false},
		{"git@github.com:o/r", "", "", false},
		{"@tools", "", "", false},
	}
	for _, tt := range tests {
		n, m, ok := ParsePluginID(tt.in)
		if n != tt.name || m != tt.mk || ok != tt.ok {
			t.Errorf("ParsePluginID(%q) = %q %q %v", tt.in, n, m, ok)
		}
	}
}

func TestParseMarketplaceSource(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		in   string
		want MarketplaceSource
	}{
		{dir, MarketplaceSource{Kind: "directory", Path: dir}},
		{"org/repo", MarketplaceSource{Kind: "github", Repo: "org/repo"}},
		{"org/repo@v1", MarketplaceSource{Kind: "github", Repo: "org/repo", Ref: "v1"}},
		{"org/repo#main", MarketplaceSource{Kind: "github", Repo: "org/repo", Ref: "main"}},
		{"https://git.example/r.git#dev", MarketplaceSource{Kind: "git", URL: "https://git.example/r.git", Ref: "dev"}},
		{"git@host:o/r.git", MarketplaceSource{Kind: "git", URL: "git@host:o/r.git"}},
		{"https://example.com/marketplace.json", MarketplaceSource{Kind: "url", URL: "https://example.com/marketplace.json"}},
	}
	for _, tt := range tests {
		got, err := ParseMarketplaceSource(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("ParseMarketplaceSource(%q) = %+v, %v", tt.in, got, err)
		}
	}
	if _, err := ParseMarketplaceSource("not a thing"); err == nil {
		t.Error("want error")
	}
}

// gitRepo makes a git repository from files and returns its path and the
// commit SHA.
func gitRepo(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	writeTree(t, dir, files)
	ctx := context.Background()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false", "add", "-A"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init"},
	} {
		if _, err := DefaultGit(ctx, dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	sha, err := DefaultGit(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return dir, sha
}

func commit(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	writeTree(t, dir, files)
	ctx := context.Background()
	for _, args := range [][]string{
		{"add", "-A"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "next"},
	} {
		if _, err := DefaultGit(ctx, dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	sha, _ := DefaultGit(ctx, dir, "rev-parse", "HEAD")
	return sha
}

func TestFetchGitSources(t *testing.T) {
	ctx := context.Background()
	repo, first := gitRepo(t, map[string]string{
		".claude-plugin/plugin.json":           `{"name":"fmt","version":"1.0.0"}`,
		"skills/fmt/SKILL.md":                  "---\ndescription: v1\n---\n",
		"tools/sub/.claude-plugin/plugin.json": `{"name":"sub"}`,
		"tools/sub/commands/go.md":             "go\n",
		"bin/run*":                             "#!/bin/sh\n",
	})
	second := commit(t, repo, map[string]string{"skills/fmt/SKILL.md": "---\ndescription: v2\n---\n"})
	url := "file://" + repo

	m := &Marketplace{Name: "mkt"}
	work := t.TempDir()

	t.Run("url at head", func(t *testing.T) {
		e := &Entry{Name: "fmt", Source: Source{Kind: SourceURL, URL: url}}
		res, err := Fetch(ctx, m, e, filepath.Join(work, "head"), FetchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if res.SHA != second {
			t.Errorf("sha = %s, want %s", res.SHA, second)
		}
		p, err := Load(res.Dir, LoadOptions{Entry: e, Marketplace: m.Name})
		if err != nil {
			t.Fatal(err)
		}
		if p.Skills[0].Description != "v2" || p.ID != "fmt@mkt" || len(p.Bin) != 1 {
			t.Errorf("plugin = %+v", p)
		}
		if fi, err := os.Stat(filepath.Join(res.Dir, "bin", "run")); err != nil || fi.Mode()&0o100 == 0 {
			t.Errorf("bin/run must stay executable: %v", err)
		}
		if _, err := os.Stat(filepath.Join(res.Dir, ".git")); err == nil {
			t.Error(".git must not be copied")
		}
	})

	t.Run("pinned sha", func(t *testing.T) {
		e := &Entry{Name: "fmt", Source: Source{Kind: SourceURL, URL: url, SHA: first}}
		res, err := Fetch(ctx, m, e, filepath.Join(work, "pinned"), FetchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		p, _ := Load(res.Dir, LoadOptions{})
		if res.SHA != first || p.Skills[0].Description != "v1" {
			t.Errorf("sha %s desc %q", res.SHA, p.Skills[0].Description)
		}
	})

	t.Run("install pin overrides entry", func(t *testing.T) {
		e := &Entry{Name: "fmt", Source: Source{Kind: SourceURL, URL: url, SHA: first}}
		res, err := Fetch(ctx, m, e, filepath.Join(work, "override"), FetchOptions{SHA: second})
		if err != nil || res.SHA != second {
			t.Fatalf("res = %+v err = %v", res, err)
		}
	})

	t.Run("git-subdir", func(t *testing.T) {
		e := &Entry{Name: "sub", Source: Source{Kind: SourceGitSubdir, URL: url, Path: "tools/sub"}}
		res, err := Fetch(ctx, m, e, filepath.Join(work, "sub"), FetchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		p, err := Load(res.Dir, LoadOptions{})
		if err != nil || p.Name != "sub" || len(p.Commands) != 1 {
			t.Errorf("plugin = %+v err = %v", p, err)
		}
		if _, err := os.Stat(filepath.Join(res.Dir, "skills")); err == nil {
			t.Error("only the subdirectory is copied")
		}
	})

	t.Run("unknown sha fails", func(t *testing.T) {
		e := &Entry{Name: "fmt", Source: Source{Kind: SourceURL, URL: url, SHA: sha40}}
		if _, err := Fetch(ctx, m, e, filepath.Join(work, "bad"), FetchOptions{}); err == nil {
			t.Error("want error")
		}
		if _, err := os.Stat(filepath.Join(work, "bad")); err == nil {
			t.Error("dest must not exist after a failed fetch")
		}
	})

	t.Run("unsupported", func(t *testing.T) {
		e := &Entry{Name: "x", Source: Source{Kind: SourceNPM, Package: "x"}}
		if _, err := Fetch(ctx, m, e, filepath.Join(work, "npm"), FetchOptions{}); !errors.Is(err, ErrUnsupportedSource) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestFetchMarketplaceAndRelative(t *testing.T) {
	ctx := context.Background()
	repo, sha := gitRepo(t, map[string]string{
		".claude-plugin/marketplace.json": `{"name":"mkt","owner":{"name":"o"},"plugins":[
			{"name":"one","source":"./plugins/one"},
			{"name":"lsp","source":"./plugins/lsp","strict":false,"lspServers":{"go":{"command":"gopls","extensionToLanguage":{".go":"go"}}}}]}`,
		"plugins/one/.claude-plugin/plugin.json": `{"name":"one"}`,
		"plugins/one/skills/a/SKILL.md":          "---\ndescription: a\n---\n",
		"plugins/lsp/README.md":                  "no manifest\n",
	})
	work := t.TempDir()
	for _, src := range []string{"file://" + repo, repo} {
		ms, err := ParseMarketplaceSource(src)
		if err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(work, ms.Kind)
		m, err := FetchMarketplace(ctx, ms, dest, FetchOptions{})
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if m.Name != "mkt" || m.SHA != sha || m.Root != dest {
			t.Errorf("%s: marketplace = %+v", src, m)
		}
		e, _ := m.Find("lsp")
		res, err := Fetch(ctx, m, e, filepath.Join(work, ms.Kind+"-lsp"), FetchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if res.SHA != sha {
			t.Errorf("relative source records the marketplace sha: %s", res.SHA)
		}
		p, err := Load(res.Dir, LoadOptions{Entry: e, Marketplace: m.Name})
		if err != nil {
			t.Fatal(err)
		}
		if p.Name != "lsp" || len(p.LSPServers) != 1 || !p.RunsCode() {
			t.Errorf("lsp plugin = %+v", p)
		}
		if _, err := Fetch(ctx, m, e, filepath.Join(work, ms.Kind+"-wrongpin"), FetchOptions{SHA: sha40}); err == nil {
			t.Error("a pin that does not match the marketplace must fail")
		}
	}
}

func TestCopyTreeSymlinks(t *testing.T) {
	src := t.TempDir()
	outside := t.TempDir()
	writeTree(t, src, map[string]string{"a.txt": "a"})
	writeTree(t, outside, map[string]string{"secret": "s"})
	if err := os.Symlink("a.txt", filepath.Join(src, "in-link")); err != nil {
		t.Skip("symlinks not supported")
	}
	_ = os.Symlink(filepath.Join(outside, "secret"), filepath.Join(src, "out-link"))
	_ = os.Symlink("../"+filepath.Base(outside)+"/secret", filepath.Join(src, "rel-out"))
	dst := filepath.Join(t.TempDir(), "d")
	if err := CopyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "in-link")); err != nil {
		t.Error("inner link kept")
	}
	for _, n := range []string{"out-link", "rel-out"} {
		if _, err := os.Lstat(filepath.Join(dst, n)); err == nil {
			t.Errorf("%s must be dropped", n)
		}
	}
}

func TestResolveRef(t *testing.T) {
	ctx := context.Background()
	repo, first := gitRepo(t, map[string]string{"a": "1"})
	if _, err := DefaultGit(ctx, repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "tag.gpgsign=false", "tag", "-a", "v1", "-m", "v1"); err != nil {
		t.Fatal(err)
	}
	second := commit(t, repo, map[string]string{"a": "2"})
	src := Source{Kind: SourceURL, URL: "file://" + repo}
	tests := []struct {
		ref  string
		src  Source
		want string
	}{
		{"", src, second},
		{"main", src, second},
		{"v1", src, first},
		{"", Source{Kind: SourceURL, URL: src.URL, SHA: first}, first},
		{"", Source{Kind: SourceURL, URL: src.URL, Ref: "v1"}, first},
		{sha40, src, sha40},
	}
	for _, tt := range tests {
		got, err := ResolveRef(ctx, tt.src, tt.ref, FetchOptions{})
		if err != nil || got != tt.want {
			t.Errorf("ResolveRef(%q, %+v) = %q, %v; want %q", tt.ref, tt.src, got, err, tt.want)
		}
	}
	if _, err := ResolveRef(ctx, src, "nope", FetchOptions{}); err == nil {
		t.Error("unknown ref")
	}
	if _, err := ResolveRef(ctx, Source{Kind: SourceNPM, Package: "x"}, "", FetchOptions{}); !errors.Is(err, ErrUnsupportedSource) {
		t.Errorf("npm: %v", err)
	}
}

func TestFetchLimits(t *testing.T) {
	ctx := context.Background()
	mroot := t.TempDir()
	writeTree(t, mroot, map[string]string{
		".claude-plugin/marketplace.json": `{"name":"m","owner":{"name":"o"},"plugins":[{"name":"p","source":"./p"}]}`,
		"p/a.md":                          "12345", "p/b.md": "12345", "p/c.md": "12345",
	})
	m, err := LoadMarketplace(mroot)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := m.Find("p")
	work := t.TempDir()
	for name, opts := range map[string]FetchOptions{"files": {MaxFiles: 2}, "bytes": {MaxBytes: 12}} {
		dest := filepath.Join(work, name)
		if _, err := Fetch(ctx, m, e, dest, opts); !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s: err = %v", name, err)
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Errorf("%s: dest left behind", name)
		}
	}
	if _, err := Fetch(ctx, m, e, filepath.Join(work, "ok"), FetchOptions{MaxFiles: 3, MaxBytes: 15}); err != nil {
		t.Errorf("at the limit: %v", err)
	}
}

func TestGitArgumentInjection(t *testing.T) {
	m, err := ParseMarketplace([]byte(`{"name":"m","owner":{"name":"o"},"plugins":[
		{"name":"a","source":{"source":"url","url":"https://x/r.git","ref":"--upload-pack=touch /tmp/pwned"}},
		{"name":"b","source":{"source":"url","url":"-oProxyCommand=x"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Plugins) != 0 || len(m.Skipped) != 2 {
		t.Fatalf("option-like refs and urls must be refused: %+v", m)
	}
	called := false
	git := func(context.Context, string, ...string) (string, error) { called = true; return "", nil }
	e := &Entry{Name: "x", Source: Source{Kind: SourceURL, URL: "https://x/r.git"}}
	if _, err := Fetch(context.Background(), &Marketplace{Name: "m"}, e, filepath.Join(t.TempDir(), "d"), FetchOptions{Ref: "-x", Git: git}); err == nil || called {
		t.Fatalf("Fetch with an option-like ref: err=%v called=%v", err, called)
	}
	if _, err := ResolveRef(context.Background(), e.Source, "-x", FetchOptions{Git: git}); err == nil || called {
		t.Fatalf("ResolveRef with an option-like ref: err=%v called=%v", err, called)
	}
}
