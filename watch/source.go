package watch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// DefaultMaxOutput is the number of bytes of output a check keeps.
	DefaultMaxOutput = 8000
	// DefaultTimeout bounds one check.
	DefaultTimeout = 30 * time.Second
	maxURLBytes    = 256 * 1024
	scriptWaitGap  = 2 * time.Second
)

// Source is what to check: exactly one of Script, URL or Job.
type Source struct {
	// Script is a command run with "sh -c". Its standard output is the
	// observed value.
	Script string
	// URL is fetched with GET. The response body is the observed value.
	URL string
	// Job is the id of a background job of the caller. The package never
	// runs or reads a job: the caller observes it and feeds the observation
	// as a Result (see Result.Exited and Result.Idle). Runner.Run refuses a
	// Job source.
	Job string
}

// Validate reports an error unless exactly one of Script, URL and Job is set.
func (s Source) Validate() error {
	n := 0
	for _, v := range []string{s.Script, s.URL, s.Job} {
		if v != "" {
			n++
		}
	}
	switch {
	case s.Job != "" && n > 1:
		return errors.New("set only one of script, url and job")
	case s.Job != "":
		return nil
	case s.Script != "" && s.URL != "":
		return errors.New("set only one of script and url")
	case n == 0:
		return errors.New("no source: set script or url")
	}
	return nil
}

// String describes the source in one short line.
func (s Source) String() string {
	if s.Job != "" {
		return "job " + oneLine(s.Job, 60)
	}
	if s.Script != "" {
		return "script " + oneLine(s.Script, 60)
	}
	return "url " + oneLine(s.URL, 80)
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		s += "..."
	}
	return s
}

// Result is the outcome of one check.
type Result struct {
	// Output is the observed value, cut to the runner's MaxOutput.
	Output string
	// Hash is the hex SHA-256 of Output. It is empty when Err is set.
	Hash string
	// ExitCode is the exit status of a script that ran to the end, else 0.
	ExitCode int
	// Exited is set by a caller that observes a background job (Source.Job)
	// when the job has exited. ExitCode is then its exit status. Runner never
	// sets it.
	Exited bool
	// Idle is how long a job that is still running has written no output.
	// A caller that observes a job sets it. Runner never does.
	Idle time.Duration
	// Err is set when the check could not be made. A script that exits with a
	// non-zero status sets an *ExitError; Output still holds what it wrote.
	Err error
}

// ExitError reports a script that ran and exited with a non-zero status.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("script exited with status %d", e.Code) }

// Runner runs checks. The zero value uses the defaults.
type Runner struct {
	// Timeout bounds one check. Zero means DefaultTimeout.
	Timeout time.Duration
	// MaxOutput is the number of bytes of output kept. Zero means
	// DefaultMaxOutput.
	MaxOutput int
	// Client fetches URLs. Nil means http.DefaultClient.
	Client *http.Client
	// Dir is the working directory of a script. Empty means the current one.
	Dir string
	// Exec runs a script in place of "sh -c", for a host that has its own
	// executor (a remote machine, a sandbox). Nil means run it locally. Dir is
	// not used when Exec is set. A script that exits with a non-zero status is
	// not an error here: see [ExecFunc].
	Exec ExecFunc
}

// ExecFunc runs script and returns what it wrote to standard output and its
// exit status. Return a non-nil err only when the script could not be run to
// the end: it did not start, or ctx was done first. A non-zero exitCode with a
// nil err is a plain value. It is part of the observation, so it feeds
// [Compare] and an exit-zero [Waiter] like the output does, and it is never
// counted as a failed check.
type ExecFunc func(ctx context.Context, script string) (stdout string, exitCode int, err error)

// ResultFromExec turns what an [ExecFunc] returned into a [Result], with the
// output cut to max bytes (zero means [DefaultMaxOutput]). When err is set the
// Result carries it and no output. Else the exit status is kept in ExitCode
// and Err stays nil, whatever the status is. A non-zero status is added to the
// Hash, so a script that prints nothing but changes its status still counts as
// a change.
func ResultFromExec(stdout string, exitCode int, err error, max int) Result {
	if err != nil {
		return Result{Err: err}
	}
	if max <= 0 {
		max = DefaultMaxOutput
	}
	out := Cap(stdout, max)
	h := Hash(out)
	if exitCode != 0 {
		h = Hash(out + "\x00exit=" + strconv.Itoa(exitCode))
	}
	return Result{Output: out, Hash: h, ExitCode: exitCode}
}

// Run checks src once with a zero Runner.
func Run(ctx context.Context, src Source) Result { return Runner{}.Run(ctx, src) }

// Run checks src once. It returns when the source answers, ctx is done or the
// timeout passes.
func (r Runner) Run(ctx context.Context, src Source) Result {
	if err := src.Validate(); err != nil {
		return Result{Err: err}
	}
	if src.Job != "" {
		return Result{Err: errors.New("a job source is observed by the caller, not run")}
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	max := r.MaxOutput
	if max <= 0 {
		max = DefaultMaxOutput
	}
	if src.Script != "" && r.Exec != nil {
		stdout, code, err := r.Exec(ctx, src.Script)
		if err != nil && ctx.Err() != nil {
			err = fmt.Errorf("script did not finish: %w", ctx.Err())
		}
		return ResultFromExec(stdout, code, err, max)
	}
	if src.Script != "" {
		return r.runScript(ctx, src.Script, max)
	}
	return r.runURL(ctx, src.URL, max)
}

func (r Runner) runScript(ctx context.Context, script string, max int) Result {
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	cmd.Dir = r.Dir
	group(cmd)
	cmd.WaitDelay = scriptWaitGap
	out, err := cmd.Output()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return finish(string(out), max)
	case ctx.Err() != nil:
		return Result{Err: fmt.Errorf("script did not finish: %w", ctx.Err())}
	case errors.As(err, &ee):
		return Result{Output: Cap(string(out), max), ExitCode: ee.ExitCode(), Err: &ExitError{Code: ee.ExitCode()}}
	default:
		return Result{Err: fmt.Errorf("script failed: %w", err)}
	}
}

func (r Runner) runURL(ctx context.Context, url string, max int) Result {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Result{Err: fmt.Errorf("url: %w", err)}
	}
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{Err: fmt.Errorf("url: %w", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return Result{Err: fmt.Errorf("url: HTTP %d", resp.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxURLBytes+1))
	if err != nil {
		return Result{Err: fmt.Errorf("url: %w", err)}
	}
	if len(body) > maxURLBytes {
		return Result{Err: fmt.Errorf("url: body exceeds %d bytes", maxURLBytes)}
	}
	return finish(string(body), max)
}

func finish(out string, max int) Result {
	out = Cap(out, max)
	return Result{Output: out, Hash: Hash(out)}
}

// Cap cuts s to at most max bytes without splitting a UTF-8 sequence.
func Cap(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// Hash returns the hex SHA-256 of s.
func Hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
