//go:build unix

package chat

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoMapNoSpecialFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", "")
	if err := unix.Mkfifo(filepath.Join(dir, "pipe.go"), 0600); err != nil {
		t.Fatal(err)
	}
	os.Symlink("/dev/zero", filepath.Join(dir, "device.go"))
	os.Mkdir(filepath.Join(dir, "nested"), 0700)
	os.WriteFile(filepath.Join(dir, "nested/.git"), []byte("gitdir: x"), 0600)
	os.WriteFile(filepath.Join(dir, "nested/hidden.go"), []byte("package main\nfunc Hidden() {}"), 0600)
	out, e := renderRepoMapWith(context.Background(), dir, 3000, testMapFactory, repoMapLimitsDefault)
	if e != nil || strings.Contains(out, "Hidden") || !strings.Contains(out, "skipped=3") {
		t.Fatalf("%s %v", out, e)
	}
	if f, e := openRepoMapRegular(filepath.Join(dir, "pipe.go")); e == nil {
		f.Close()
		t.Fatal("FIFO accepted")
	}
	out, e = renderRepoMapWith(context.Background(), filepath.Join(dir, "nested"), 3000, testMapFactory, repoMapLimitsDefault)
	if e != nil || !strings.Contains(out, "Hidden") {
		t.Fatalf("explicit nested: %s %v", out, e)
	}
}
