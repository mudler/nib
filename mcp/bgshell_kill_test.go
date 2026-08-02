// Process groups and signal delivery are POSIX concepts. nib's non-test code
// still builds on Windows, so this file is excluded there rather than breaking
// `GOOS=windows go vet ./mcp/...`, matching app/signal_test.go.

//go:build unix

package mcp

import (
	"context"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// waitJobWithin is waitJob with a caller-chosen deadline. The kill tests want a
// tight bound because the bug they cover is precisely that a killed job stays
// "running" for the command's full duration.
func waitJobWithin(t *testing.T, j *bgJob, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if done, _, _ := j.snapshot(); done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s still not done %v after kill", j.id, within)
}

// The deterministic version of TestBgJobKill.
//
// /bin/sh is dash on the CI box, and `sh -c 'sleep N'` FORKS rather than execs,
// so the shell is the direct child and the sleep is a grandchild that inherited
// the stdout/stderr pipe write ends. exec.CommandContext's default cancel kills
// only the direct child, and cmd.Wait() does not return until those pipes reach
// EOF — so an orphaned grandchild held Wait open for the command's whole
// lifetime, and the job stayed "running" long after it was killed.
//
// TestBgJobKill flaked at ~40% because it kills IMMEDIATELY: when the cancel won
// the race against dash's fork there was no grandchild to orphan and everything
// looked fine. Sleeping past the fork removes the race and makes the bug
// deterministic.
// The deadline is deliberately SHORTER than bgJobWaitDelay. With a looser bound
// this test passes even with configureJobProcess stubbed out, because WaitDelay
// alone eventually unblocks Wait — it would be a WaitDelay test wearing a
// process-group test's name. Under one second, only an actual group kill can
// satisfy it.
func TestBgJobKillStopsAShellThatForkedAChild(t *testing.T) {
	if bgJobWaitDelay <= time.Second {
		t.Fatalf("bgJobWaitDelay is %v; this test's 1s bound no longer proves the group kill did the work", bgJobWaitDelay)
	}
	// The bug needs a shell that FORKS its command. Inheriting SHELL_CMD could
	// substitute one that execs instead (bash does for a lone command), and the
	// test would pass regardless of the fix.
	t.Setenv("SHELL_CMD", "")

	mgr := newBgJobManager()
	j := mgr.launch(context.Background(), "sleep 30", false)

	// Past the fork on purpose. Killing before it is the case that passed even
	// with the bug present, and is why the original test flaked instead of
	// failing outright.
	time.Sleep(250 * time.Millisecond)

	if !mgr.kill(j.id) {
		t.Fatal("kill returned false for a known job")
	}

	waitJobWithin(t, j, time.Second)

	if st := j.status(); st == "completed" {
		t.Fatalf("a killed job should not report completed, got %q", st)
	}
}

// Marking the job done is bookkeeping; this is the part the user actually cares
// about. Killing a background build or server must stop the work, not merely
// stop tracking it. Before the fix the grandchild survived the kill and ran to
// completion.
func TestBgJobKillTerminatesTheWholeProcessTree(t *testing.T) {
	if _, err := exec.LookPath("pgrep"); err != nil {
		// This is the ONLY test that fails when the group kill is reverted, so
		// skipping it silently would leave the fix uncovered. Fail on Linux,
		// where pgrep is expected, rather than pretending we checked.
		if runtime.GOOS == "linux" {
			t.Fatal("pgrep is missing: the only test that covers the process-group kill cannot run")
		}
		t.Skip("pgrep not available on this host")
	}
	t.Setenv("SHELL_CMD", "")

	// A duration nothing else on the machine is plausibly sleeping for, so
	// pgrep cannot match an unrelated process.
	const marker = "31.41592653"

	mgr := newBgJobManager()
	j := mgr.launch(context.Background(), "sleep "+marker, false)
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", "sleep "+marker).Run() })

	// Wait until the grandchild actually exists, so the test proves the kill
	// reached a running process rather than winning a race against the fork.
	if !waitForProcess(t, marker, 3*time.Second) {
		t.Fatalf("the shell never forked `sleep %s`; the test cannot prove anything", marker)
	}

	if !mgr.kill(j.id) {
		t.Fatal("kill returned false for a known job")
	}
	waitJobWithin(t, j, 3*time.Second)

	if stillRunning(t, marker) {
		t.Fatalf("`sleep %s` survived the kill: the job was marked dead but the work kept running", marker)
	}
}

// bgJobWaitDelay bounds how long Wait blocks on output pipes a lingering child
// still holds. That is necessary, but it must not turn a command that SUCCEEDED
// into a reported failure: Wait returns exec.ErrWaitDelay in that case, which is
// not an *exec.ExitError, so the naive mapping stamps exit -1 and status
// "failed". An agent told that `npm run dev &` failed will try to fix a script
// that worked.
func TestBgJobSucceedsWhenAChildOutlivesIt(t *testing.T) {
	t.Setenv("SHELL_CMD", "")

	mgr := newBgJobManager()
	// The script exits immediately; the backgrounded sleep keeps the inherited
	// stdout/stderr write ends open, so Wait cannot finish until WaitDelay.
	j := mgr.launch(context.Background(), "echo done; sleep 20 &", false)
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", "^sleep 20$").Run() })

	waitJobWithin(t, j, bgJobWaitDelay+2*time.Second)

	done, code, errMsg := j.snapshot()
	if !done {
		t.Fatal("job never finished")
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0: the command succeeded, only a child outlived it", code)
	}
	if errMsg != "" {
		t.Fatalf("errMsg = %q, want empty: WaitDelay is not a command failure", errMsg)
	}
	if st := j.status(); st != "completed" {
		t.Fatalf("status = %q, want completed", st)
	}
	if out := j.stdout.String(); !strings.Contains(out, "done") {
		t.Fatalf("stdout = %q, want it to contain the command's output", out)
	}
}

// waitForProcess polls until a process whose command line contains marker
// exists, reporting whether it appeared before the deadline.
func waitForProcess(t *testing.T, marker string, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if stillRunning(t, marker) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// stillRunning reports whether the `sleep <marker>` GRANDCHILD is alive.
//
// The pattern is anchored on purpose. An unanchored `sleep <marker>` also
// matches the parent `/bin/sh -c sleep <marker>`, so it would report true as
// soon as the shell exists — before the fork this test needs to have happened,
// and again for a shell that outlived a killed child. Anchoring makes it match
// the sleep process alone.
//
// pgrep exits 1 with no output when nothing matches, which is not an error.
func stillRunning(t *testing.T, marker string) bool {
	t.Helper()
	out, _ := exec.Command("pgrep", "-f", "^sleep "+regexp.QuoteMeta(marker)+"$").Output()
	return strings.TrimSpace(string(out)) != ""
}
