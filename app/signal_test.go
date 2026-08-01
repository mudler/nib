package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	childModeEnv = "NIB_APP_TEST_CHILD_MODE"
	childArgsEnv = "NIB_APP_TEST_CHILD_ARGS"
)

// TestMain doubles as the entrypoint of the child processes the tests in this
// file spawn.
//
// Signal disposition is a whole-process property. Whether app.run installed a
// handler is not observable from inside the same process: if it did not, the
// signal kills the test binary, and if the test registers a handler of its own
// to survive that, both branches look identical. So these tests re-exec this
// binary in a child, signal it for real, and assert on how it died.
func TestMain(m *testing.M) {
	switch os.Getenv(childModeEnv) {
	case "main":
		os.Exit(Main(append([]string{"nib"}, childArgs()...)))
	case "run":
		err := Run(context.Background(), Options{Args: childArgs()})
		var exit ExitError
		switch {
		case errors.As(err, &exit):
			os.Exit(exit.Code)
		case err != nil:
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func childArgs() []string {
	return strings.Fields(os.Getenv(childArgsEnv))
}

// childProc is a re-exec of the test binary running either Main or Run.
type childProc struct {
	cmd     *exec.Cmd
	stderr  string
	done    chan struct{}
	waitErr error
}

// startChild re-execs the test binary with mode "main" (app.Main) or "run"
// (app.Run with a plain context.Background), passing args as nib's argv tail.
// xdg becomes both XDG_CONFIG_HOME and HOME, so the child reads only the
// config the test wrote.
func startChild(t *testing.T, mode, args, xdg string) *childProc {
	t.Helper()

	errPath := filepath.Join(t.TempDir(), "child-stderr.log")
	errFile, err := os.Create(errPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { errFile.Close() })

	c := exec.Command(os.Args[0])
	c.Env = append(os.Environ(),
		childModeEnv+"="+mode,
		childArgsEnv+"="+args,
		"XDG_CONFIG_HOME="+xdg,
		"HOME="+xdg,
		"MODEL=",
		"API_KEY=",
		"BASE_URL=",
		"NIB_TRACE_DIR=",
		"NIB_YOLO=",
	)
	// An open, never-written stdin keeps the serving modes parked in their read
	// rather than seeing an immediate EOF.
	if _, err := c.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	c.Stdout = io.Discard
	c.Stderr = errFile
	// Own process group: the fixture's helper processes (an MCP server that
	// sleeps) are cleaned up wholesale, while the signals the tests send still
	// go to the nib process alone.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := c.Start(); err != nil {
		t.Fatal(err)
	}

	p := &childProc{cmd: c, stderr: errPath, done: make(chan struct{})}
	go func() {
		p.waitErr = c.Wait()
		close(p.done)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		<-p.done
	})
	return p
}

func (p *childProc) running() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (p *childProc) signal(t *testing.T, sig syscall.Signal) {
	t.Helper()
	if !p.running() {
		t.Fatalf("child exited before it could be signalled: %v\nstderr:\n%s", p.waitErr, p.stderrText())
	}
	if err := p.cmd.Process.Signal(sig); err != nil {
		t.Fatalf("sending %v: %v", sig, err)
	}
}

// awaitExit waits for the child to exit and returns its wait status. It fails
// the test if the child outlives the timeout, which is what "the signal was
// swallowed" looks like from out here.
func (p *childProc) awaitExit(t *testing.T, within time.Duration) syscall.WaitStatus {
	t.Helper()
	select {
	case <-p.done:
	case <-time.After(within):
		t.Fatalf("child still running %s after the signal: it was swallowed\nstderr:\n%s", within, p.stderrText())
	}
	st, ok := p.cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("unexpected wait status type %T", p.cmd.ProcessState.Sys())
	}
	return st
}

func (p *childProc) stderrText() string {
	b, err := os.ReadFile(p.stderr)
	if err != nil {
		return "<unreadable: " + err.Error() + ">"
	}
	return string(b)
}

// childConfigDir writes cfg as the child's user config.yaml and returns the
// directory to hand it as XDG_CONFIG_HOME.
func childConfigDir(t *testing.T, cfg string) string {
	t.Helper()
	xdg := t.TempDir()
	root := filepath.Join(xdg, "nib")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return xdg
}

func waitForFile(t *testing.T, path string, within time.Duration, p *childProc) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if !p.running() {
			t.Fatalf("child exited before reaching the blocking call: %v\nstderr:\n%s", p.waitErr, p.stderrText())
		}
		if time.Now().After(deadline) {
			t.Fatalf("child never reached the blocking call within %s\nstderr:\n%s", within, p.stderrText())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// hangingMCPConfig configures one stdio MCP server that touches marker and then
// sleeps, so `nib mcp test hang` parks inside its connect handshake. It is a
// local subprocess, not a network call.
func hangingMCPConfig(marker string) string {
	return fmt.Sprintf(`model: test-model
mcp_servers:
  hang:
    command: sh
    args: ["-c", "touch %s; exec sleep 60"]
`, marker)
}

// Standalone nib dispatched `plugin`, `skill` and the `mcp` management verbs
// before it installed any handler, so Ctrl+C killed them outright. Installing
// the handler first and then dispatching to functions that take no context
// leaves the signal with nothing to cancel: the process just keeps going, and
// `nib skill install` over a slow network needs a kill -9.
func TestMainDoesNotSwallowSIGINTDuringAManagementSubcommand(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "connecting")
	xdg := childConfigDir(t, hangingMCPConfig(marker))

	p := startChild(t, "main", "mcp test hang", xdg)
	waitForFile(t, marker, 30*time.Second, p)

	p.signal(t, syscall.SIGINT)

	st := p.awaitExit(t, 10*time.Second)
	if !st.Signaled() || st.Signal() != syscall.SIGINT {
		t.Fatalf("child exited %v (signaled=%v), want death by SIGINT\nstderr:\n%s", st, st.Signaled(), p.stderrText())
	}
}

// The same for SIGTERM, which the branch's handler also intercepted.
func TestMainDoesNotSwallowSIGTERMDuringAManagementSubcommand(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "connecting")
	xdg := childConfigDir(t, hangingMCPConfig(marker))

	p := startChild(t, "main", "mcp test hang", xdg)
	waitForFile(t, marker, 30*time.Second, p)

	p.signal(t, syscall.SIGTERM)

	st := p.awaitExit(t, 10*time.Second)
	if !st.Signaled() || st.Signal() != syscall.SIGTERM {
		t.Fatalf("child exited %v (signaled=%v), want death by SIGTERM\nstderr:\n%s", st, st.Signaled(), p.stderrText())
	}
}

// The counterpart: outside the management subcommands, Main must still catch
// SIGINT and cancel the run, which is what makes the TUI and the serving modes
// shut down cleanly instead of being killed mid-write.
func TestMainStillCatchesSIGINTOutsideTheManagementSubcommands(t *testing.T) {
	xdg := childConfigDir(t, "model: test-model\n")

	// `nib mcp` with no subcommand serves the agent over stdio and parks there
	// until its context is cancelled.
	p := startChild(t, "main", "mcp", xdg)
	time.Sleep(2 * time.Second)

	p.signal(t, syscall.SIGINT)

	st := p.awaitExit(t, 15*time.Second)
	if st.Signaled() {
		t.Fatalf("child died from %v: Main no longer installs its handler\nstderr:\n%s", st.Signal(), p.stderrText())
	}
}

// Run is the embedder's entrypoint: it takes cancellation from the context its
// caller passes and must never touch process-wide signal disposition, or an
// embedded nib would quietly take over its host's Ctrl+C.
func TestRunInstallsNoSignalHandler(t *testing.T) {
	xdg := childConfigDir(t, "model: test-model\n")

	p := startChild(t, "run", "mcp", xdg)
	time.Sleep(2 * time.Second)

	p.signal(t, syscall.SIGINT)

	st := p.awaitExit(t, 15*time.Second)
	if !st.Signaled() || st.Signal() != syscall.SIGINT {
		t.Fatalf("child exited %v (signaled=%v), want death by SIGINT: Run installed a handler\nstderr:\n%s",
			st, st.Signaled(), p.stderrText())
	}
}
