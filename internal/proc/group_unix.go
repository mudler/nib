// Process groups and kill(-pgid) are POSIX concepts. nib's non-test code still
// builds on Windows, so the portable no-op lives in group_other.go rather than
// this file being the only definition.

//go:build unix

package proc

import (
	"os/exec"
	"syscall"
)

// Group makes cmd run in its own process group and cancel by killing that whole
// group. It must be called before cmd.Start, which is when the context watchdog
// captures Cancel.
//
// Without it, exec.CommandContext's default cancel kills only the DIRECT child.
// Every caller here runs its command through `sh -c`, and /bin/sh (dash) forks
// the command rather than exec'ing it — so the real work is a grandchild that
// survives the kill, keeps running, and keeps the stdout/stderr pipe write ends
// it inherited open. cmd.Wait does not return until those pipes reach EOF, so
// cancelling neither stopped the work nor promptly unblocked the caller.
//
// Setpgid puts the child in a new group whose id is its own pid, so kill(-pid)
// reaches the shell and everything it spawned, and nothing else.
//
// Two behavior notes for callers. The command no longer shares nib's process
// group, so a terminal-generated SIGINT no longer reaches it directly;
// cancellation still does, through the context. And the group is terminated
// with SIGKILL, so a command that would previously have received a graceful
// SIGINT gets no chance to clean up.
//
// Group does not set cmd.WaitDelay. A command that daemonizes with setsid()
// escapes the group and can still hold the pipes open, so callers that must not
// block indefinitely set their own delay with their own rationale.
func Group(cmd *exec.Cmd) {
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
