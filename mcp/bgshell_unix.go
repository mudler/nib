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
// Two behavior changes worth knowing. The job no longer shares nib's process
// group, so a terminal-generated SIGINT no longer reaches it directly;
// cancellation still does, through the context, which is the path nib relies on
// for both Ctrl+C and bash_job_kill. And the group is terminated with SIGKILL,
// so a job that previously got a graceful SIGINT from the terminal no longer
// gets a chance to clean up — a server or container the agent started is killed
// outright rather than asked to stop.
func configureJobProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pid := cmd.Process.Pid

		// Only address the group when this pid really is its own group leader.
		//
		// Two reasons. Cmd.Wait reaps the pid before it receives the
		// cancellation result, so a late Cancel can run against a freed pid;
		// os.Process.Kill guards that, a raw syscall.Kill does not, and on pid
		// wraparound -pid would signal an unrelated group. And if SysProcAttr
		// above were ever overwritten so Setpgid did not take effect, the child
		// would share NIB'S group — where -pid means "kill nib, its children,
		// and the terminal's foreground group". Checking the pgid makes both
		// impossible instead of merely unlikely.
		if pgid, err := syscall.Getpgid(pid); err == nil && pgid == pid {
			if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil {
				return nil
			}
		}
		return cmd.Process.Kill()
	}
}
