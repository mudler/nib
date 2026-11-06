//go:build unix

package watch

import (
	"os/exec"
	"syscall"
	"time"
)

// group runs cmd in its own process group and, on cancel, ends the whole
// group: "sh -c" may fork the real command, and killing only the shell would
// leave it running with the output pipe open.
func group(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pid := cmd.Process.Pid
		// Address the group only when this pid leads its own group.
		if pgid, err := syscall.Getpgid(pid); err == nil && pgid == pid {
			if err := syscall.Kill(-pid, syscall.SIGTERM); err == nil {
				time.AfterFunc(time.Second, func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
				return nil
			}
		}
		return cmd.Process.Kill()
	}
}
