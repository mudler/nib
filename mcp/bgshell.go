package mcp

import (
	"bytes"
	"context"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// bgMaxOutput caps the captured output per stream for a background job, so a
// chatty long-running command can't grow memory without bound.
const bgMaxOutput = 64 * 1024

// lockedBuffer is a concurrency-safe, size-capped writer: the running command's
// goroutine writes to it while tool handlers read it. Once the cap is hit
// further writes are dropped and the buffer is marked truncated.
type lockedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	truncated bool
}

func (w *lockedBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if room := bgMaxOutput - w.buf.Len(); room > 0 {
		if len(p) > room {
			w.buf.Write(p[:room])
			w.truncated = true
		} else {
			w.buf.Write(p)
		}
	} else {
		w.truncated = true
	}
	return len(p), nil // always report full consumption so the command isn't blocked
}

func (w *lockedBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.buf.String()
	if w.truncated {
		s += "\n…[output truncated]"
	}
	return s
}

// bgJob is a single background shell command.
type bgJob struct {
	id      string
	script  string
	stdout  lockedBuffer
	stderr  lockedBuffer
	cancel  context.CancelFunc
	started time.Time

	mu       sync.Mutex
	done     bool
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

// bgJobManager tracks background shell jobs for a session.
type bgJobManager struct {
	mu   sync.Mutex
	jobs map[string]*bgJob
	seq  int
}

func newBgJobManager() *bgJobManager { return &bgJobManager{jobs: map[string]*bgJob{}} }

// start launches script under a context derived from parent (so the job
// survives a single turn but is cancelled when the session/app shuts down) and
// returns immediately.
func (m *bgJobManager) start(parent context.Context, script string) *bgJob {
	m.mu.Lock()
	m.seq++
	id := "bg-" + strconv.Itoa(m.seq)
	m.mu.Unlock()

	ctx, cancel := context.WithCancel(parent)
	j := &bgJob{id: id, script: script, cancel: cancel, started: time.Now()}

	shellExec, shellArgs := shellInvocation(script)
	cmd := exec.CommandContext(ctx, shellExec, shellArgs...)
	cmd.Stdout = &j.stdout
	cmd.Stderr = &j.stderr

	if err := cmd.Start(); err != nil {
		cancel()
		j.mu.Lock()
		j.done, j.exitCode, j.errMsg = true, -1, err.Error()
		j.mu.Unlock()
	} else {
		go func() {
			err := cmd.Wait()
			j.mu.Lock()
			j.done = true
			if err != nil {
				if ee, ok := err.(*exec.ExitError); ok {
					j.exitCode = ee.ExitCode()
				} else {
					j.exitCode = -1
				}
				j.errMsg = err.Error()
			}
			j.mu.Unlock()
		}()
	}

	m.mu.Lock()
	m.jobs[id] = j
	m.mu.Unlock()
	return j
}

func (m *bgJobManager) get(id string) (*bgJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	return j, ok
}

func (m *bgJobManager) list() []*bgJob {
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

// shellInvocation resolves the configured shell (SHELL_CMD, default "sh -c")
// into an executable plus args for running script.
func shellInvocation(script string) (string, []string) {
	parts := strings.Fields(getShellCommand())
	if len(parts) > 1 {
		return parts[0], append(append([]string{}, parts[1:]...), script)
	}
	return parts[0], []string{"-c", script}
}

// --- MCP tool I/O shapes ---

type bgStartInput struct {
	Script string `json:"script" jsonschema:"the shell script to run in the background"`
}
type bgStartOutput struct {
	JobID   string `json:"job_id" jsonschema:"id of the started background job"`
	Message string `json:"message" jsonschema:"human-readable status"`
}

type bgJobRefInput struct {
	JobID string `json:"job_id" jsonschema:"id of a background job (from bash_background or bash_jobs)"`
}

type bgOutputResult struct {
	JobID    string `json:"job_id"`
	Status   string `json:"status" jsonschema:"running, completed, or failed"`
	Done     bool   `json:"done"`
	ExitCode int    `json:"exit_code" jsonschema:"exit code (valid once done)"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Error    string `json:"error,omitempty"`
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

// registerBackgroundShellTools wires the background-shell tools onto server.
// Jobs run under srvCtx (the session/server lifetime), not a per-call context,
// so they keep running after the tool call (and the turn) returns.
func registerBackgroundShellTools(srvCtx context.Context, server *mcp.Server) {
	mgr := newBgJobManager()

	mcp.AddTool(server, &mcp.Tool{
		Name:        "bash_background",
		Description: "Run a shell script in the background and return immediately with a job_id. Use this for long-running commands (servers, builds, watchers, downloads) so the conversation isn't blocked. Read progress with bash_job_output and stop it with bash_job_kill.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in bgStartInput) (*mcp.CallToolResult, bgStartOutput, error) {
		j := mgr.start(srvCtx, in.Script)
		return nil, bgStartOutput{JobID: j.id, Message: "Started background job " + j.id}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "bash_jobs",
		Description: "List background shell jobs started with bash_background and their status (running/completed/failed).",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, bgListOutput, error) {
		var out bgListOutput
		for _, j := range mgr.list() {
			out.Jobs = append(out.Jobs, bgJobInfo{JobID: j.id, Script: j.script, Status: j.status()})
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "bash_job_output",
		Description: "Read the captured stdout/stderr and status of a background shell job by job_id.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in bgJobRefInput) (*mcp.CallToolResult, bgOutputResult, error) {
		j, ok := mgr.get(in.JobID)
		if !ok {
			return nil, bgOutputResult{JobID: in.JobID, Status: "unknown", Error: "no such job"}, nil
		}
		done, code, errMsg := j.snapshot()
		return nil, bgOutputResult{
			JobID:    j.id,
			Status:   j.status(),
			Done:     done,
			ExitCode: code,
			Stdout:   j.stdout.String(),
			Stderr:   j.stderr.String(),
			Error:    errMsg,
		}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "bash_job_kill",
		Description: "Stop a running background shell job by job_id.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in bgJobRefInput) (*mcp.CallToolResult, bgKillOutput, error) {
		if !mgr.kill(in.JobID) {
			return nil, bgKillOutput{JobID: in.JobID, Killed: false, Message: "no such job"}, nil
		}
		return nil, bgKillOutput{JobID: in.JobID, Killed: true, Message: "Killed background job " + in.JobID}, nil
	})
}
