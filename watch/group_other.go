//go:build !unix

package watch

import "os/exec"

// group is a no-op where process groups do not exist.
func group(cmd *exec.Cmd) {}
