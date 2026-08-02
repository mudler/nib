// Process groups and pgrep are POSIX concepts. nib's non-test code still builds
// on Windows, so this file is excluded there rather than breaking
// `GOOS=windows go vet ./hooks/...`, matching app/signal_test.go.

//go:build unix

package hooks

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mudler/nib/types"
)

// A hook that exits 0 but leaves a child running keeps the stdout/stderr pipes
// it inherited open, so cmd.Run returns exec.ErrWaitDelay once WaitDelay fires.
// runHook mapped ANY non-nil error to Block, so a perfectly successful hook
// blocked the user's tool call — the worst possible outcome for a mechanism
// whose job is to allow or deny.
func TestFireDoesNotBlockWhenAHookLeavesAChildRunning(t *testing.T) {
	const marker = "27.18281828"
	dir := t.TempDir()
	// The hook itself succeeds immediately; the backgrounded child outlives it
	// and holds the pipes past WaitDelay.
	script := writeScript(t, dir, "ok.sh", "sleep "+marker+" & echo '{\"approved\": true}'")
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", "^sleep "+regexp.QuoteMeta(marker)+"$").Run() })

	d := New([]types.HookConfig{{Event: "PreToolUse", Command: script, Dir: dir}})
	got := d.Fire(context.Background(), EventPreToolUse, "bash", nil)

	if len(got) != 1 {
		t.Fatalf("expected one decision, got %+v", got)
	}
	if got[0].Block {
		t.Fatalf("a hook that exited 0 must not block just because a child outlived it: %+v", got[0])
	}
	if got[0].Approved == nil || !*got[0].Approved {
		t.Fatalf("the hook's own decision was lost: %+v", got[0])
	}
}

// When a hook times out, killing only the shell leaves whatever it spawned
// running forever. The hook is gone, nobody is tracking the child, and it holds
// the pipes that made the timeout slow in the first place.
func TestFireTimeoutKillsTheHooksProcessTree(t *testing.T) {
	if _, err := exec.LookPath("pgrep"); err != nil {
		t.Skip("pgrep not available on this host")
	}
	const marker = "23.14069263"
	dir := t.TempDir()
	// The child outlives the hook; the hook itself never finishes, so the
	// context deadline is what ends it.
	script := writeScript(t, dir, "leak.sh", "sleep "+marker+" & sleep 30")
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", "^sleep "+regexp.QuoteMeta(marker)+"$").Run() })

	d := New([]types.HookConfig{{Event: "PreToolUse", Command: script, Dir: dir}})
	d.timeout = 200 * time.Millisecond

	got := d.Fire(context.Background(), EventPreToolUse, "bash", nil)
	if len(got) != 1 || !got[0].Block {
		t.Fatalf("a timed-out hook should still Block: %+v", got)
	}

	// Give the group kill a moment to be reaped, then require the child gone.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processMatching(t, marker) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("`sleep %s` survived the hook timeout: the hook's process tree was leaked", marker)
}

// processMatching reports whether a `sleep <marker>` process is alive. The
// pattern is anchored so it cannot also match the parent `/bin/sh -c ...`,
// which would make the assertion pass for the wrong reason.
func processMatching(t *testing.T, marker string) bool {
	t.Helper()
	out, _ := exec.Command("pgrep", "-f", "^sleep "+regexp.QuoteMeta(marker)+"$").Output()
	return strings.TrimSpace(string(out)) != ""
}
