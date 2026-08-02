//go:build !unix

package proc

import "os/exec"

// Group is a no-op off Unix: process groups and kill(-pgid) do not exist there.
// exec.CommandContext's default cancel — terminate the direct child — stays in
// force, and a caller's WaitDelay still bounds cmd.Wait so a surviving
// grandchild holding the output pipes cannot block forever. See group_unix.go
// for what this buys on POSIX systems.
func Group(cmd *exec.Cmd) {}
