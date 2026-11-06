package promise

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The package is meant to be imported by other modules, which cannot import an
// internal package. Keep it free of any package of this module.
func TestDependsOnNoPackageOfThisModule(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go tool not found")
	}
	out, err := exec.Command(goBin, "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("go list failed: %v", err)
	}
	for _, p := range strings.Fields(string(out)) {
		if strings.HasPrefix(p, "github.com/mudler/nib") && p != "github.com/mudler/nib/promise" {
			t.Errorf("promise depends on %s", p)
		}
	}
}

// A separate module with a replace directive can import and call the package.
func TestImportableFromAnotherModule(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go tool not found")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/consumer\n\ngo 1.21\n\nrequire github.com/mudler/nib v0.0.0\n\nreplace github.com/mudler/nib => "+root+"\n")
	write("main.go", `package main

import (
	"fmt"
	"os"

	"github.com/mudler/nib/promise"
)

func main() {
	if !promise.Promises("I'll check on it shortly.") || promise.Promises("Do you want me to check later?") {
		fmt.Println("unexpected result")
		os.Exit(1)
	}
}
`)
	cmd := exec.Command(goBin, "run", "-mod=mod", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off", "GOPROXY=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("consumer module failed: %v\n%s", err, out)
	}
}
