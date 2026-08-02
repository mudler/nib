// Process groups and kill(-pgid) are POSIX concepts. nib's non-test code still
// builds on Windows, so the portable no-op lives in bgshell_other.go rather
// than this file being the only definition.

//go:build unix

package mcp

import (
	"os/exec"
	"syscall"
)

// configureJobProcess puts a launched shell job in its own process group and
// makes cancellation kill that entire group.
//
// Without it, exec.CommandContext's default cancel kills only the DIRECT child
// — the shell. /bin/sh (dash) forks the command rather than exec'ing it, so a
// `sleep 30` is a grandchild that survives the kill, keeps running, and keeps
// the stdout/stderr pipe write ends it inherited open. cmd.Wait() does not
// return until those pipes reach EOF, so the job stayed "running" for the
// command's full lifetime: bash_job_kill reported success, bash_jobs kept
// listing it, HasRunning kept parking the agent run, and the work never
// actually stopped.
//
// Setpgid puts the child in a new group whose id is its own pid, so kill(-pid)
// reaches the shell and everything it spawned, and nothing else. This is the
// same pattern app/signal_test.go uses to manage its child processes.
//
// One behavior change worth knowing: the job no longer shares nib's process
// group, so a terminal-generated SIGINT no longer reaches it directly.
// Cancellation still does, through the context — which is the path nib relies
// on for both Ctrl+C and bash_job_kill.
func configureJobProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// A negative pid addresses the process group. If the group is already
		// gone, fall back to the single process so a racing exit is not
		// reported as a cancellation failure.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
