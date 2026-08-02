// Process groups and signal delivery are POSIX concepts. nib's non-test code
// still builds on Windows, so this file is excluded there rather than breaking
// `GOOS=windows go vet ./mcp/...`, matching app/signal_test.go.

//go:build unix

package mcp

import (
	"context"
	"os/exec"
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
func TestBgJobKillStopsAShellThatForkedAChild(t *testing.T) {
	mgr := newBgJobManager()
	j := mgr.launch(context.Background(), "sleep 30", false)

	// Past the fork on purpose. Killing before it is the case that passed even
	// with the bug present.
	time.Sleep(250 * time.Millisecond)

	if !mgr.kill(j.id) {
		t.Fatal("kill returned false for a known job")
	}

	waitJobWithin(t, j, 3*time.Second)

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
		t.Skip("pgrep not available on this host")
	}

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

// stillRunning reports whether any process command line contains marker.
// pgrep exits 1 with no output when nothing matches, which is not an error.
func stillRunning(t *testing.T, marker string) bool {
	t.Helper()
	out, _ := exec.Command("pgrep", "-f", "sleep "+marker).Output()
	return strings.TrimSpace(string(out)) != ""
}
