//go:build !unix

package mcp

import "os/exec"

// configureJobProcess is a no-op off Unix: process groups and kill(-pgid) do not
// exist there. exec.CommandContext's default cancel — terminate the direct
// child — stays in force, and bgJobWaitDelay still bounds cmd.Wait() so a
// surviving grandchild holding the output pipes cannot hang a killed job
// forever. See bgshell_unix.go for what this buys on POSIX systems.
func configureJobProcess(cmd *exec.Cmd) {}
