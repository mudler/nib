package mcp

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/nib/internal/proc"
)

// bgMaxOutput caps the captured output per stream for a shell job, so a chatty
// long-running command can't grow memory without bound. Past the cap the
// oldest bytes are dropped: the tail is where errors and results end up.
const bgMaxOutput = 200 * 1024

// bgMaxPage caps one bash_job_output page, so a single read of a large
// buffer cannot flood the context.
const bgMaxPage = 64 * 1024

// Finished jobs are forgotten once they are bgJobTTL old, or when more than
// bgMaxJobs are kept; running jobs are never pruned.
const (
	bgJobTTL  = 30 * time.Minute
	bgMaxJobs = 64
)

// bgJobWaitDelay bounds how long cmd.Wait() may block on the output pipes after
// the process itself has finished or been cancelled.
//
// configureJobProcess already kills the whole process group on Unix, which
// handles the ordinary case of a shell that forked its command. This is the
// backstop for what that cannot reach: a job that daemonizes with setsid()
// escapes the group, survives the kill, and keeps the inherited stdout/stderr
// write ends open — and cmd.Wait() does not return until those pipes reach EOF.
// Without a delay, one such job would leave a killed job "running" forever,
// parking the agent on work nobody is waiting for.
const bgJobWaitDelay = 2 * time.Second

// lockedBuffer is a concurrency-safe, size-capped writer: the running command's
// goroutine writes to it while tool handlers / the UI read it. Once the cap is
// hit it keeps the last bgMaxOutput bytes and counts the ones it dropped, so
// a byte offset into the whole output stays valid while the window moves.
type lockedBuffer struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	dropped int // bytes discarded from the front
}

func (w *lockedBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	if excess := w.buf.Len() - bgMaxOutput; excess > 0 {
		w.buf.Next(excess)
		w.dropped += excess
	}
	return len(p), nil // always report full consumption so the command isn't blocked
}

func (w *lockedBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.buf.String()
	if w.dropped > 0 {
		s = "…[earlier output truncated]\n" + s
	}
	return s
}

// page returns up to limit bytes of the output starting at the absolute byte
// offset (counted from the first byte the command ever wrote). An offset that
// fell out of the window is moved up to the oldest byte still kept. The page
// never splits a UTF-8 sequence. It returns the page, the offset it actually
// starts at, and the total bytes written so far.
func (w *lockedBuffer) page(offset, limit int) (string, int, int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	b := w.buf.Bytes()
	total := w.dropped + len(b)
	start := offset - w.dropped
	if start < 0 {
		start = 0
	}
	if start > len(b) {
		start = len(b)
	}
	for start < len(b) && !utf8.RuneStart(b[start]) {
		start++
	}
	end := len(b)
	if limit > 0 && start+limit < end {
		end = start + limit
		for end > start && !utf8.RuneStart(b[end]) {
			end--
		}
	}
	return string(b[start:end]), w.dropped + start, total
}

// bgJob is a single shell command. A job may be started directly in the
// background (detach == nil) or run in the foreground as a detachable job
// (detach != nil) that the user can background mid-run with Ctrl+B.
type bgJob struct {
	id      string
	script  string
	stdout  lockedBuffer
	stderr  lockedBuffer
	cancel  context.CancelFunc
	started time.Time

	detach chan struct{} // non-nil while the job is a detachable foreground job
	doneCh chan struct{} // closed when the process exits

	mu       sync.Mutex
	done     bool
	ended    time.Time // when done became true
	detached bool      // set when backgrounded via Ctrl+B (guarded by manager.mu)
	exitCode int
	errMsg   string
}

func (j *bgJob) status() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	switch {
	case !j.done:
		return "running"
	case j.exitCode == 0 && j.errMsg == "":
		return "completed"
	default:
		return "failed"
	}
}

func (j *bgJob) snapshot() (done bool, code int, errMsg string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.done, j.exitCode, j.errMsg
}

// toOutput renders the job as a bash-tool result.
func (j *bgJob) toOutput(script string, limits *OutputLimitsPolicy, artifacts *ArtifactStore) executeCommandOutput {
	_, code, errMsg := j.snapshot()
	l := limits.Resolved()
	return executeCommandOutput{
		Script:   script,
		Stdout:   LimitOutput(j.stdout.String(), "bash", l, artifacts),
		Stderr:   LimitOutput(j.stderr.String(), "bash", l, artifacts),
		ExitCode: code,
		Success:  code == 0 && errMsg == "",
		Error:    errMsg,
	}
}

// bgJobManager tracks shell jobs for a session.
type bgJobManager struct {
	mu   sync.Mutex
	jobs map[string]*bgJob
	seq  int

	// onDone, when set, is invoked once for each job that finishes, from the
	// job's wait goroutine. Used to push a completion notice into the live run's
	// message-injection channel (cogito has no concept of shell jobs).
	onDone func(*bgJob)

	// dir, when non-empty, is the working directory launched commands run in
	// (cmd.Dir). Empty means the process cwd (legacy behavior).
	dir string
}

func newBgJobManager() *bgJobManager { return newBgJobManagerInDir("") }
func newBgJobManagerInDir(dir string) *bgJobManager {
	return &bgJobManager{jobs: map[string]*bgJob{}, dir: dir}
}

// launch starts script under a context derived from parent (so the job survives
// a single turn but is cancelled when the session/app shuts down). When
// foreground is true the job carries a detach channel so it can be backgrounded
// mid-run. It returns immediately; the caller decides whether to wait.
func (m *bgJobManager) launch(parent context.Context, script string, foreground bool) *bgJob {
	m.mu.Lock()
	m.seq++
	id := "bg-" + strconv.Itoa(m.seq)
	m.mu.Unlock()
	m.prune(time.Now())

	ctx, cancel := context.WithCancel(parent)
	j := &bgJob{id: id, script: script, cancel: cancel, started: time.Now(), doneCh: make(chan struct{})}
	if foreground {
		j.detach = make(chan struct{}, 1)
	}

	shellExec, shellArgs := shellInvocation(script)
	cmd := exec.CommandContext(ctx, shellExec, shellArgs...)
	if m.dir != "" {
		cmd.Dir = m.dir
	}
	cmd.Stdout = &j.stdout
	cmd.Stderr = &j.stderr

	// Both must be set before Start: Start captures Cancel and WaitDelay when it
	// arms the context watchdog.
	proc.Group(cmd)
	cmd.WaitDelay = bgJobWaitDelay

	if err := cmd.Start(); err != nil {
		cancel()
		j.mu.Lock()
		j.done, j.exitCode, j.errMsg = true, -1, err.Error()
		j.ended = time.Now()
		j.mu.Unlock()
		close(j.doneCh)
		m.notifyDone(j)
	} else {
		go func() {
			err := cmd.Wait()
			j.mu.Lock()
			j.done = true
			j.ended = time.Now()
			switch {
			case err == nil:
				// Clean exit, pipes closed on their own.
			case errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil:
				// The command itself finished; WaitDelay fired only because
				// something it spawned still held the inherited output pipes
				// open. That is not a failure of the command, and reporting it
				// as one would tell the model to "fix" a script that worked —
				// `npm run dev &` and any other job that leaves a helper
				// running would come back as exit -1. Report the process's real
				// status instead, and only carry an error when it actually
				// failed.
				j.exitCode = cmd.ProcessState.ExitCode()
				if j.exitCode != 0 {
					j.errMsg = err.Error()
				}
			default:
				if ee, ok := err.(*exec.ExitError); ok {
					j.exitCode = ee.ExitCode()
				} else {
					j.exitCode = -1
				}
				j.errMsg = err.Error()
			}
			j.mu.Unlock()
			close(j.doneCh)
			m.notifyDone(j)
		}()
	}

	m.mu.Lock()
	m.jobs[id] = j
	m.mu.Unlock()
	return j
}

// notifyDone invokes the registered completion hook (if any) for a finished job.
// Called from the job's wait goroutine, after j.done is set.
func (m *bgJobManager) notifyDone(j *bgJob) {
	m.mu.Lock()
	cb := m.onDone
	m.mu.Unlock()
	if cb != nil {
		cb(j)
	}
}

// backgrounded reports whether a job runs detached from a turn: a bash_background
// job (detach == nil) or a foreground job the user backgrounded with Ctrl+B
// (detached). Reads detach/detached under m.mu, matching List.
func (m *bgJobManager) backgrounded(j *bgJob) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return j.detach == nil || j.detached
}

// hasRunning reports whether any tracked shell job is still running.
func (m *bgJobManager) hasRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if done, _, _ := j.snapshot(); !done {
			return true
		}
	}
	return false
}

func (m *bgJobManager) get(id string) (*bgJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	return j, ok
}

func (m *bgJobManager) ordered() []*bgJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*bgJob, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, j)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].started.Before(out[b].started) })
	return out
}

// kill cancels a job's context (terminating the process). Returns false if the
// id is unknown.
func (m *bgJobManager) kill(id string) bool {
	j, ok := m.get(id)
	if !ok {
		return false
	}
	j.cancel()
	return true
}

// wait blocks until the job exits, the timeout elapses or ctx is done, and
// reports whether the job was found.
func (m *bgJobManager) wait(ctx context.Context, id string, timeout time.Duration) (*bgJob, bool) {
	j, ok := m.get(id)
	if !ok {
		return nil, false
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-j.doneCh:
	case <-timer.C:
	case <-ctx.Done():
	}
	return j, true
}

// prune forgets finished jobs that ended more than bgJobTTL before now, then
// the oldest finished jobs while more than bgMaxJobs remain. Running jobs are
// never pruned: the model or the user may still read or kill them.
func (m *bgJobManager) prune(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var finished []*bgJob
	for id, j := range m.jobs {
		j.mu.Lock()
		done, ended := j.done, j.ended
		j.mu.Unlock()
		if !done {
			continue
		}
		if now.Sub(ended) > bgJobTTL {
			delete(m.jobs, id)
			continue
		}
		finished = append(finished, j)
	}
	if excess := len(m.jobs) - bgMaxJobs; excess > 0 {
		sort.Slice(finished, func(a, b int) bool { return finished[a].ended.Before(finished[b].ended) })
		for i := 0; i < excess && i < len(finished); i++ {
			delete(m.jobs, finished[i].id)
		}
	}
}

// detachForeground backgrounds the most-recently-started running foreground job
// (the one a Ctrl+B targets) by signalling its detach channel. Returns the job
// id and true when one was detached.
func (m *bgJobManager) detachForeground() (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var pick *bgJob
	for _, j := range m.jobs {
		if j.detach == nil || j.detached {
			continue
		}
		if done, _, _ := j.snapshot(); done {
			continue
		}
		if pick == nil || j.started.After(pick.started) {
			pick = j
		}
	}
	if pick == nil {
		return "", false
	}
	pick.detached = true
	select {
	case pick.detach <- struct{}{}:
	default:
	}
	return pick.id, true
}

func (m *bgJobManager) hasForeground() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.detach == nil || j.detached {
			continue
		}
		if done, _, _ := j.snapshot(); !done {
			return true
		}
	}
	return false
}

// shellInvocation resolves the configured shell (SHELL_CMD, default "sh -c")
// into an executable plus args for running script.
func shellInvocation(script string) (string, []string) {
	parts := strings.Fields(getShellCommand())
	if len(parts) > 1 {
		return parts[0], append(append([]string{}, parts[1:]...), script)
	}
	return parts[0], []string{"-c", script}
}

// ShellJobs is the shared registry of shell jobs. It is created in main.go and
// shared between the shell MCP server (which starts/manages jobs) and the UI
// (which lists jobs for the footer and backgrounds the foreground one on
// Ctrl+B).
type ShellJobs struct {
	mgr *bgJobManager
}

// NewShellJobs creates an empty shared shell-job registry.
func NewShellJobs() *ShellJobs { return &ShellJobs{mgr: newBgJobManager()} }

// NewShellJobsInDir creates a shell-job registry whose commands run in dir
// (cmd.Dir). An empty dir preserves the legacy process-cwd behavior.
func NewShellJobsInDir(dir string) *ShellJobs { return &ShellJobs{mgr: newBgJobManagerInDir(dir)} }

// ShellJobInfo is a UI-facing snapshot of a shell job.
type ShellJobInfo struct {
	ID      string
	Script  string
	Status  string // running | completed | failed
	Running bool
	// Backgrounded is true for jobs that run detached from a turn — started via
	// bash_background, or a foreground command the user backgrounded with Ctrl+B.
	// A normal foreground command (consumed inline by the turn) is false.
	Backgrounded bool
}

// List returns all shell jobs in start order, oldest first.
func (s *ShellJobs) List() []ShellJobInfo {
	if s == nil {
		return nil
	}
	m := s.mgr
	m.mu.Lock()
	type row struct {
		j  *bgJob
		bg bool
	}
	rows := make([]row, 0, len(m.jobs))
	for _, j := range m.jobs {
		// detach/detached are read here under m.mu (detachForeground writes them
		// under the same lock).
		rows = append(rows, row{j: j, bg: j.detach == nil || j.detached})
	}
	m.mu.Unlock()

	sort.Slice(rows, func(a, b int) bool { return rows[a].j.started.Before(rows[b].j.started) })
	out := make([]ShellJobInfo, 0, len(rows))
	for _, r := range rows {
		done, _, _ := r.j.snapshot()
		out = append(out, ShellJobInfo{ID: r.j.id, Script: r.j.script, Status: r.j.status(), Running: !done, Backgrounded: r.bg})
	}
	return out
}

// Output returns the captured stdout/stderr of a shell job by id.
func (s *ShellJobs) Output(id string) (stdout, stderr string, ok bool) {
	if s == nil {
		return "", "", false
	}
	j, found := s.mgr.get(id)
	if !found {
		return "", "", false
	}
	return j.stdout.String(), j.stderr.String(), true
}

// DetachForeground backgrounds the running foreground shell command (Ctrl+B).
func (s *ShellJobs) DetachForeground() (string, bool) {
	if s == nil {
		return "", false
	}
	return s.mgr.detachForeground()
}

// HasForeground reports whether a detachable foreground shell command is
// currently running.
func (s *ShellJobs) HasForeground() bool {
	return s != nil && s.mgr.hasForeground()
}

// HasRunning reports whether any shell job (background or foreground) is still
// running. Used as a WithPendingWork predicate so the live agent run parks while
// a backgrounded shell command is still in flight.
func (s *ShellJobs) HasRunning() bool {
	return s != nil && s.mgr.hasRunning()
}

// SetOnJobDone registers a callback invoked once for each shell job that
// finishes (from the job's wait goroutine). The session uses it to inject a
// completion notice into the live run. fn receives a UI-facing snapshot; it is
// called for every job — the caller decides whether to act (e.g. only on
// backgrounded jobs). Safe to call once at setup.
func (s *ShellJobs) SetOnJobDone(fn func(ShellJobInfo)) {
	if s == nil {
		return
	}
	s.mgr.mu.Lock()
	defer s.mgr.mu.Unlock()
	if fn == nil {
		s.mgr.onDone = nil
		return
	}
	mgr := s.mgr
	mgr.onDone = func(j *bgJob) {
		done, _, _ := j.snapshot()
		fn(ShellJobInfo{
			ID:           j.id,
			Script:       j.script,
			Status:       j.status(),
			Running:      !done,
			Backgrounded: mgr.backgrounded(j),
		})
	}
}

// Kill stops a shell job by id.
func (s *ShellJobs) Kill(id string) bool { return s != nil && s.mgr.kill(id) }

// --- MCP tool I/O shapes ---

type bgJobRefInput struct {
	JobID string `json:"job_id" jsonschema:"id of a background job (from bash_background or bash_jobs)"`
}

type bgOutputInput struct {
	JobID  string `json:"job_id" jsonschema:"id of a background job (from bash_background or bash_jobs)"`
	Offset int    `json:"offset,omitempty" jsonschema:"byte offset into the job's whole stdout to start reading at (default 0). Pass next_offset from the previous page to continue."`
	Limit  int    `json:"limit,omitempty" jsonschema:"max bytes of stdout to return (default and max 65536)"`
}

type bgWaitInput struct {
	JobID   string `json:"job_id" jsonschema:"id of a background job (from bash_background or bash_jobs)"`
	Timeout int    `json:"timeout,omitempty" jsonschema:"max seconds to wait (default 60, max 600)"`
}

type bgOutputResult struct {
	JobID    string `json:"job_id"`
	Status   string `json:"status" jsonschema:"running, completed, or failed"`
	Done     bool   `json:"done"`
	ExitCode int    `json:"exit_code" jsonschema:"exit code (valid once done)"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Error    string `json:"error,omitempty"`
	// Paging over stdout. Offsets count from the first byte the job wrote,
	// so they stay valid after old output is dropped.
	Offset     int  `json:"offset,omitempty" jsonschema:"byte offset this stdout page starts at"`
	NextOffset int  `json:"next_offset,omitempty" jsonschema:"offset to pass to read the next page"`
	TotalBytes int  `json:"total_bytes,omitempty" jsonschema:"stdout bytes the job has written so far"`
	TimedOut   bool `json:"timed_out,omitempty" jsonschema:"bash_job_wait only: the job was still running when the wait ended"`
}

// outputResult renders a job for bash_job_output and bash_job_wait.
func outputResult(j *bgJob, offset, limit int, limits *OutputLimitsPolicy, artifacts *ArtifactStore) bgOutputResult {
	if limit <= 0 || limit > bgMaxPage {
		limit = bgMaxPage
	}
	done, code, errMsg := j.snapshot()
	stdout, start, total := j.stdout.page(offset, limit)
	l := limits.Resolved()
	res := bgOutputResult{
		JobID:      j.id,
		Status:     j.status(),
		Done:       done,
		ExitCode:   code,
		Stdout:     LimitOutput(stdout, "bash_job_output", l, artifacts),
		Stderr:     LimitOutput(j.stderr.String(), "bash_job_output", l, artifacts),
		Error:      errMsg,
		Offset:     start,
		NextOffset: start + len(stdout),
		TotalBytes: total,
	}
	if start > offset {
		res.Stdout = "…[output before byte " + strconv.Itoa(start) + " was dropped]\n" + res.Stdout
	}
	return res
}

type bgStartInput struct {
	Script string `json:"script" jsonschema:"the shell script to run in the background"`
}
type bgStartOutput struct {
	JobID   string `json:"job_id" jsonschema:"id of the started background job"`
	Message string `json:"message" jsonschema:"human-readable status"`
}

type bgJobInfo struct {
	JobID  string `json:"job_id"`
	Script string `json:"script"`
	Status string `json:"status"`
}
type bgListOutput struct {
	Jobs []bgJobInfo `json:"jobs"`
}

type bgKillOutput struct {
	JobID   string `json:"job_id"`
	Killed  bool   `json:"killed"`
	Message string `json:"message"`
}

// registerBackgroundShellTools wires the explicit background-shell tools onto
// server, backed by the shared manager mgr. Jobs run under srvCtx so they keep
// running after the tool call (and the turn) returns.
func registerBackgroundShellTools(srvCtx context.Context, server *mcp.Server, mgr *bgJobManager, limits *OutputLimitsPolicy, artifacts *ArtifactStore) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "bash_background",
		Description: "Run a shell script in the background and return immediately with a job_id. Use this for long-running commands (servers, builds, watchers, downloads) so the conversation isn't blocked. Read progress with bash_job_output and stop it with bash_job_kill.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in bgStartInput) (*mcp.CallToolResult, bgStartOutput, error) {
		j := mgr.launch(srvCtx, in.Script, false)
		return nil, bgStartOutput{JobID: j.id, Message: "Started background job " + j.id}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "bash_jobs",
		Description: "List shell jobs (background or backgrounded) and their status (running/completed/failed).",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, bgListOutput, error) {
		out := bgListOutput{Jobs: []bgJobInfo{}}
		for _, j := range mgr.ordered() {
			out.Jobs = append(out.Jobs, bgJobInfo{JobID: j.id, Script: j.script, Status: j.status()})
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "bash_job_output",
		Description: "Read the captured stdout/stderr and status of a shell job by job_id. Stdout comes in pages of up to 64KB: when next_offset is below total_bytes, call again with offset=next_offset to read more. Only the last 200KB of output is kept.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in bgOutputInput) (*mcp.CallToolResult, bgOutputResult, error) {
		j, ok := mgr.get(in.JobID)
		if !ok {
			return nil, bgOutputResult{JobID: in.JobID, Status: "unknown", Error: "no such job"}, nil
		}
		return nil, outputResult(j, in.Offset, in.Limit, limits, artifacts), nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "bash_job_wait",
		Description: "Block until a shell job exits or the timeout elapses, then return its status and output like bash_job_output. Use this instead of polling bash_job_output in a loop. If timed_out is true the job is still running: wait again or read its output.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in bgWaitInput) (*mcp.CallToolResult, bgOutputResult, error) {
		timeout := time.Duration(in.Timeout) * time.Second
		if in.Timeout <= 0 {
			timeout = 60 * time.Second
		}
		if timeout > 600*time.Second {
			timeout = 600 * time.Second
		}
		j, ok := mgr.wait(ctx, in.JobID, timeout)
		if !ok {
			return nil, bgOutputResult{JobID: in.JobID, Status: "unknown", Error: "no such job"}, nil
		}
		res := outputResult(j, 0, 0, limits, artifacts)
		// Read from the tail: after a wait the end of the output matters most.
		if res.TotalBytes > bgMaxPage {
			res = outputResult(j, res.TotalBytes-bgMaxPage, 0, limits, artifacts)
		}
		res.TimedOut = !res.Done
		return nil, res, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "bash_job_kill",
		Description: "Stop a running shell job by job_id.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in bgJobRefInput) (*mcp.CallToolResult, bgKillOutput, error) {
		if !mgr.kill(in.JobID) {
			return nil, bgKillOutput{JobID: in.JobID, Killed: false, Message: "no such job"}, nil
		}
		return nil, bgKillOutput{JobID: in.JobID, Killed: true, Message: "Killed background job " + in.JobID}, nil
	})
}
