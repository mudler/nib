package chat

import (
	"context"
	"errors"
	"fmt"
	"github.com/mudler/nib/codeindex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderRepoMap_BasicFormat(t *testing.T) {
	dir := t.TempDir()

	// Two Go files with a couple of definitions each.
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(`package main

type Foo struct{}

func Bar() {}
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte(`package main

type Baz struct{}

func Quux() {}
`), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := renderRepoMapWith(context.Background(), dir, 3000, testMapFactory, repoMapLimitsDefault)
	if err != nil {
		t.Fatalf("renderRepoMap failed: %v", err)
	}

	// Each file path should appear, along with the Section: Name (line N) lines.
	for _, want := range []string{
		"Foo",
		"Bar",
		"Baz",
		"Quux",
		"(line ",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in output:\n%s", want, out)
		}
	}

	// Lines should be indented with two spaces, like "  Function: Bar (line 5)".
	lines := strings.Split(out, "\n")
	var foundIndented bool
	for _, l := range lines {
		if strings.HasPrefix(l, "  ") && strings.Contains(l, "(line ") {
			foundIndented = true
			break
		}
	}
	if !foundIndented {
		t.Fatalf("expected an indented definition line in output:\n%s", out)
	}
}

func TestRenderRepoMap_SkipDirs(t *testing.T) {
	dir := t.TempDir()

	// A real source file at the top level.
	if err := os.WriteFile(filepath.Join(dir, "real.go"), []byte(`package main
func Real() {}
`), 0644); err != nil {
		t.Fatal(err)
	}

	// A .go file inside node_modules that must be skipped.
	nodeDir := filepath.Join(dir, "node_modules", "pkg")
	if err := os.MkdirAll(nodeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeDir, "hidden.go"), []byte(`package main
func Hidden() {}
`), 0644); err != nil {
		t.Fatal(err)
	}

	// A .go file inside .git that must be skipped.
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config.go"), []byte(`package main
func Skipped() {}
`), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := renderRepoMapWith(context.Background(), dir, 3000, testMapFactory, repoMapLimitsDefault)
	if err != nil {
		t.Fatalf("renderRepoMap failed: %v", err)
	}

	if !strings.Contains(out, "Real") {
		t.Fatalf("expected Real in output:\n%s", out)
	}
	if strings.Contains(out, "Hidden") {
		t.Fatalf("node_modules file should be skipped:\n%s", out)
	}
	if strings.Contains(out, "Skipped") {
		t.Fatalf(".git file should be skipped:\n%s", out)
	}
}

func TestRepoMapToolDefinition(t *testing.T) {
	def := repoMapToolDefinition("/work", func(p string) string { return p })
	name := def.Tool().Function.Name
	if name != "repo_map" {
		t.Fatalf("expected tool name \"repo_map\", got %q", name)
	}
}

// Explicit injection prevents starting the Go test binary as a CLI worker.
type testMapWorker struct{}

func (testMapWorker) ParseSource(ctx context.Context, name string, src []byte) ([]codeindex.Entry, bool, error) {
	return codeindex.Entries(name)
}
func (testMapWorker) Close() error                 { return nil }
func testMapFactory(context.Context) repoMapWorker { return testMapWorker{} }

func TestRepoMapBounds(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.go", "b.go", "c.go"} {
		os.WriteFile(filepath.Join(dir, n), []byte("package main\nfunc Hello() {}\n"), 0600)
	}
	limits := repoMapLimitsDefault
	limits.files = 1
	out, err := renderRepoMapWith(context.Background(), dir, 3000, testMapFactory, limits)
	if err != nil || !strings.Contains(out, "file limit") || !strings.Contains(out, "Hello") {
		t.Fatalf("%s %v", out, err)
	}
	out, err = renderRepoMapWith(context.Background(), dir, 32, testMapFactory, repoMapLimitsDefault)
	if err != nil || len(out) > 128 {
		t.Fatalf("output budget: %d %v", len(out), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err = renderRepoMapWith(ctx, dir, 3000, testMapFactory, repoMapLimitsDefault)
	if err != nil || !strings.Contains(out, "user cancelled") {
		t.Fatalf("%s %v", out, err)
	}
}

func TestRepoMapFallbackIgnoreAndRegular(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", "")
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored/\n*.go\n!keep.go\n"), 0600)
	for _, n := range []string{"keep.go", "hidden.go", "ignored/keep.go", "nested/keep.go", "other/keep.go"} {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, n)), 0700)
		os.WriteFile(filepath.Join(dir, n), []byte("package main\nfunc Visible() {}\n"), 0600)
	}
	os.WriteFile(filepath.Join(dir, "nested/.gitignore"), []byte("keep.go\n"), 0600)
	os.Symlink(filepath.Join(dir, "keep.go"), filepath.Join(dir, "link.go"))
	out, err := renderRepoMapWith(context.Background(), dir, 3000, testMapFactory, repoMapLimitsDefault)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"hidden.go", "ignored/keep.go", "nested/keep.go", "link.go"} {
		if strings.Contains(out, n) {
			t.Fatal(out)
		}
	}
	if !strings.Contains(out, "other/keep.go") {
		t.Fatal(out)
	}
}

func TestRepoMapGitScopes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if b, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("git: %s %v", b, e)
		}
	}
	git("init", "-q")
	write := func(n, body string) {
		t.Helper()
		p := filepath.Join(dir, n)
		os.MkdirAll(filepath.Dir(p), 0700)
		if e := os.WriteFile(p, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
	}
	src := "package main\nfunc Definition() {}\n"
	for _, n := range []string{"tracked.go", "ignored.go", "global.py", "sub/keep.go", "sub/hidden.go", "outside.go", "nested/hidden.go"} {
		write(n, src)
	}
	git("add", "tracked.go")
	write(".gitignore", "*.go\n!sub/\n!outside.go\n!nested/\n")
	write("sub/.gitignore", "!keep.go\n")
	global := filepath.Join(t.TempDir(), "ignore")
	os.WriteFile(global, []byte("global.py\n"), 0600)
	git("config", "core.excludesFile", global)
	write("nested/.git", "gitdir: invalid\n")
	out, e := renderRepoMapWith(context.Background(), dir, 3000, testMapFactory, repoMapLimitsDefault)
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range []string{"tracked.go", "sub/keep.go", "outside.go"} {
		if !strings.Contains(out, n) {
			t.Fatalf("missing %s: %s", n, out)
		}
	}
	for _, n := range []string{"ignored.go", "global.py", "sub/hidden.go", "nested/hidden.go"} {
		if strings.Contains(out, n) {
			t.Fatal(out)
		}
	}
	out, e = renderRepoMapWith(context.Background(), filepath.Join(dir, "sub"), 3000, testMapFactory, repoMapLimitsDefault)
	if e != nil || !strings.Contains(out, "keep.go") || strings.Contains(out, "outside.go") {
		t.Fatalf("%s %v", out, e)
	}
}

type failingMapWorker struct {
	calls, closes int
	wait          bool
}

func (w *failingMapWorker) ParseSource(ctx context.Context, _ string, _ []byte) ([]codeindex.Entry, bool, error) {
	w.calls++
	if w.wait {
		<-ctx.Done()
		return nil, false, ctx.Err()
	}
	return nil, false, errors.New("broken")
}
func (w *failingMapWorker) Close() error { w.closes++; return nil }
func TestRepoMapWorkerFailureAndDeadline(t *testing.T) {
	for _, wait := range []bool{false, true} {
		t.Run(fmt.Sprint(wait), func(t *testing.T) {
			dir := t.TempDir()
			for _, n := range []string{"a.go", "b.go"} {
				os.WriteFile(filepath.Join(dir, n), []byte("package main"), 0600)
			}
			w := &failingMapWorker{wait: wait}
			starts := 0
			limits := repoMapLimitsDefault
			limits.duration = 50 * time.Millisecond
			out, e := renderRepoMapWith(context.Background(), dir, 3000, func(context.Context) repoMapWorker { starts++; return w }, limits)
			if e != nil || starts != 1 || w.calls != 1 || w.closes != 1 {
				t.Fatalf("%s %v %+v starts=%d", out, e, w, starts)
			}
			reason := "worker failed"
			if wait {
				reason = "overall deadline"
			}
			if !strings.Contains(out, reason) {
				t.Fatal(out)
			}
		})
	}
}

func TestRepoMapReadAndEntryLimits(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", "")
	os.WriteFile(filepath.Join(dir, "huge.go"), make([]byte, codeindex.WorkerMaxSourceBytes+1), 0600)
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\nfunc Small() {}"), 0600)
	out, e := renderRepoMapWith(context.Background(), dir, 3000, testMapFactory, repoMapLimitsDefault)
	if e != nil || !strings.Contains(out, "Small") || !strings.Contains(out, "skipped=1") {
		t.Fatalf("%s %v", out, e)
	}
	for _, kind := range []string{"bytes", "entries"} {
		limits := repoMapLimitsDefault
		if kind == "bytes" {
			limits.bytes = 5
		} else {
			limits.scanned = 1
		}
		out, e = renderRepoMapWith(context.Background(), dir, 3000, testMapFactory, limits)
		reason := "byte limit"
		if kind == "entries" {
			reason = "entry limit"
		}
		if e != nil || !strings.Contains(out, reason) {
			t.Fatalf("%s %v", out, e)
		}
	}
	// Actual read budget holds even when the advertised size is below the file cap.
	s := &mapScan{ctx: context.Background(), limits: repoMapLimitsDefault}
	s.limits.bytes = 7
	_, e = s.read(filepath.Join(dir, "a.go"))
	if e == nil || s.bytes != 7 {
		t.Fatalf("read=%d err=%v", s.bytes, e)
	}
}

func TestRepoMapRunContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tool := &repoMapTool{resolvePath: func(p string) string { return t.TempDir() }, workerFactory: testMapFactory}
	out, _, err := tool.RunContext(ctx, map[string]any{})
	if err != nil || !strings.Contains(out, "user cancelled") {
		t.Fatalf("%s %v", out, err)
	}
}
