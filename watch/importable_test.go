package watch

import (
	"os/exec"
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
		if strings.HasPrefix(p, "github.com/mudler/nib") && p != "github.com/mudler/nib/watch" {
			t.Errorf("watch depends on %s", p)
		}
	}
}
