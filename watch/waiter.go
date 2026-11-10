package watch

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Mode is the condition a Waiter waits for.
type Mode int

const (
	// UntilChange waits for the output to differ from the first successful
	// check.
	UntilChange Mode = iota
	// UntilMatch waits for the output to match Wait.Pattern.
	UntilMatch
	// UntilExitZero waits for a script to exit with status 0. It needs a
	// Script source.
	UntilExitZero
	// UntilExit waits for a job to exit, with any status. It needs a Job
	// source. The caller feeds Result.Exited and Result.ExitCode.
	UntilExit
	// UntilStall waits for a job that is still running to write no output
	// for Wait.Idle. It needs a Job source and an Idle. The caller feeds
	// Result.Idle. A job that has exited never stalls: the wait ends with
	// ReasonExited instead.
	UntilStall
)

func (m Mode) String() string {
	switch m {
	case UntilChange:
		return "change"
	case UntilMatch:
		return "match"
	case UntilExitZero:
		return "exit-zero"
	case UntilExit:
		return "exit"
	case UntilStall:
		return "stall"
	}
	return "unknown"
}

// DefaultMaxErrors is how many failed checks in a row end a wait.
const DefaultMaxErrors = 3

// Wait describes what to wait for.
type Wait struct {
	Source Source
	Mode   Mode
	// Pattern is a regular expression, used by UntilMatch.
	Pattern string
	// Idle is the silence that ends an UntilStall wait. It must be positive
	// for UntilStall.
	Idle time.Duration
	// Timeout ends the wait. It must be positive.
	Timeout time.Duration
	// MaxErrors is how many failed checks in a row end the wait. Zero means
	// DefaultMaxErrors.
	MaxErrors int
}

// Reason says why a wait ended.
type Reason string

const (
	ReasonChanged    Reason = "changed"
	ReasonMatched    Reason = "matched"
	ReasonExitedZero Reason = "exited-zero"
	// ReasonExited means a job exited. Detail has its exit status.
	ReasonExited Reason = "exited"
	// ReasonStalled means a running job wrote nothing for Wait.Idle.
	ReasonStalled       Reason = "stalled"
	ReasonSourceFailing Reason = "source-failing"
	ReasonTimedOut      Reason = "timed-out"
)

// Satisfied reports whether the condition was met, as opposed to a failure or
// a timeout.
func (r Reason) Satisfied() bool {
	return r == ReasonChanged || r == ReasonMatched || r == ReasonExitedZero || r == ReasonExited || r == ReasonStalled
}

// Verdict is the state of a wait after a check.
type Verdict struct {
	// Done is true when the wait is over. Reason and Detail are set only then.
	Done   bool
	Reason Reason
	// Detail is one or two lines for a reader: the matching output, the diff,
	// the last error.
	Detail string
}

// Waiter turns repeated checks into one verdict. Create it with NewWaiter and
// call Step after each check. A Waiter is not safe for concurrent use.
type Waiter struct {
	w        Wait
	re       *regexp.Regexp
	deadline time.Time
	base     Snapshot
	errs     int
	lastErr  error
	last     string
	checks   int
}

// WaiterState is the private state of a Waiter, in a form that can be stored.
// Take it with [Waiter.State] and give it to [RestoreWaiter] to resume a wait
// after a restart or on another host. It does not hold the [Wait]: the caller
// keeps that and passes it again. The JSON field names are part of the API,
// and fields may be added but not renamed. A missing field reads as its zero
// value.
type WaiterState struct {
	// BaselineHash and BaselineOutput are the snapshot that UntilChange
	// compares with. Both are empty until the first successful check.
	BaselineHash   string `json:"baseline_hash,omitempty"`
	BaselineOutput string `json:"baseline_output,omitempty"`
	// Errors is how many failed checks in a row there are.
	Errors int `json:"errors,omitempty"`
	// Deadline is when the wait times out.
	Deadline time.Time `json:"deadline"`
	// Checks is how many checks were fed to Step.
	Checks int `json:"checks,omitempty"`
	// LastError is the message of the last failed check in the current
	// streak, and LastOutput the output of the last successful check. A
	// timeout reports them.
	LastError  string `json:"last_error,omitempty"`
	LastOutput string `json:"last_output,omitempty"`
}

// State returns the state of the Waiter. Take it after each Step to be able to
// resume.
func (wt *Waiter) State() WaiterState {
	st := WaiterState{
		BaselineHash:   wt.base.Hash,
		BaselineOutput: wt.base.Output,
		Errors:         wt.errs,
		Deadline:       wt.deadline,
		Checks:         wt.checks,
		LastOutput:     wt.last,
	}
	if wt.lastErr != nil {
		st.LastError = wt.lastErr.Error()
	}
	return st
}

// RestoreWaiter rebuilds a Waiter from w and a state taken with
// [Waiter.State]. The next Step behaves as it would have on the Waiter the
// state came from. It fails when w is invalid, as NewWaiter does, or when the
// state is not usable: no deadline, or a negative count.
func RestoreWaiter(w Wait, st WaiterState) (*Waiter, error) {
	if st.Deadline.IsZero() {
		return nil, errors.New("state has no deadline")
	}
	if st.Errors < 0 || st.Checks < 0 {
		return nil, errors.New("state has a negative count")
	}
	wt, err := NewWaiter(w, st.Deadline.Add(-w.Timeout))
	if err != nil {
		return nil, err
	}
	wt.deadline = st.Deadline
	wt.base = Snapshot{Hash: st.BaselineHash, Output: st.BaselineOutput}
	wt.errs = st.Errors
	wt.checks = st.Checks
	wt.last = st.LastOutput
	if st.LastError != "" {
		wt.lastErr = errors.New(st.LastError)
	}
	return wt, nil
}

// Checks is how many checks were fed to Step.
func (wt *Waiter) Checks() int { return wt.checks }

// NewWaiter validates w and starts the wait at now.
func NewWaiter(w Wait, now time.Time) (*Waiter, error) {
	if err := w.Source.Validate(); err != nil {
		return nil, err
	}
	if w.Timeout <= 0 {
		return nil, errors.New("timeout must be positive")
	}
	if w.MaxErrors <= 0 {
		w.MaxErrors = DefaultMaxErrors
	}
	wt := &Waiter{w: w, deadline: now.Add(w.Timeout)}
	switch w.Mode {
	case UntilChange:
	case UntilMatch:
		re, err := regexp.Compile(w.Pattern)
		if err != nil {
			return nil, fmt.Errorf("pattern: %w", err)
		}
		wt.re = re
	case UntilExitZero:
		if w.Source.Script == "" {
			return nil, errors.New("exit-zero needs a script source")
		}
	case UntilExit:
		if w.Source.Job == "" {
			return nil, errors.New("exit needs a job source")
		}
	case UntilStall:
		if w.Source.Job == "" {
			return nil, errors.New("stall needs a job source")
		}
		if w.Idle <= 0 {
			return nil, errors.New("stall needs a positive idle")
		}
	default:
		return nil, errors.New("unknown mode")
	}
	return wt, nil
}

// Deadline is when the wait times out.
func (wt *Waiter) Deadline() time.Time { return wt.deadline }

// Wait returns the description the Waiter was created with.
func (wt *Waiter) Wait() Wait { return wt.w }

// Step feeds the result of one check made at now. Once it returns Done, the
// Waiter is finished: do not call Step again.
func (wt *Waiter) Step(res Result, now time.Time) Verdict {
	wt.checks++
	if v, ok := wt.evaluate(res); ok {
		return v
	}
	if !now.Before(wt.deadline) {
		d := fmt.Sprintf("timed out after %s without the condition being met", wt.w.Timeout.Round(time.Second))
		if wt.lastErr != nil {
			d += "; last error: " + wt.lastErr.Error()
		} else if wt.last != "" {
			d += "; last output: " + oneLine(wt.last, 200)
		}
		return Verdict{Done: true, Reason: ReasonTimedOut, Detail: d}
	}
	return Verdict{}
}

// evaluate applies the condition. ok is true when the wait is over.
func (wt *Waiter) evaluate(res Result) (Verdict, bool) {
	var ee *ExitError
	if res.Err != nil && !(wt.w.Mode == UntilExitZero && errors.As(res.Err, &ee)) {
		wt.errs++
		wt.lastErr = res.Err
		if wt.errs >= wt.w.MaxErrors {
			return Verdict{Done: true, Reason: ReasonSourceFailing,
				Detail: fmt.Sprintf("%d checks in a row failed; last error: %v", wt.errs, res.Err)}, true
		}
		return Verdict{}, false
	}
	wt.errs, wt.lastErr = 0, nil
	wt.last = res.Output

	if res.Exited && (wt.w.Mode == UntilExit || wt.w.Mode == UntilStall) {
		return Verdict{Done: true, Reason: ReasonExited,
			Detail: fmt.Sprintf("job exited with status %d%s", res.ExitCode, tail(res.Output))}, true
	}

	switch wt.w.Mode {
	case UntilStall:
		if !res.Exited && res.Idle >= wt.w.Idle {
			return Verdict{Done: true, Reason: ReasonStalled,
				Detail: fmt.Sprintf("job still running but wrote no output for %s%s", res.Idle.Round(time.Second), tail(res.Output))}, true
		}
	case UntilExitZero:
		// A host executor reports a non-zero status as a value (Err nil,
		// ExitCode set), the Runner as an *ExitError.
		if res.Err == nil && res.ExitCode == 0 {
			return Verdict{Done: true, Reason: ReasonExitedZero, Detail: "script exited with status 0" + tail(res.Output)}, true
		}
	case UntilMatch:
		if loc := wt.re.FindStringIndex(res.Output); loc != nil {
			return Verdict{Done: true, Reason: ReasonMatched,
				Detail: fmt.Sprintf("output matched %q: %s", wt.w.Pattern, oneLine(res.Output[loc[0]:loc[1]], 200))}, true
		}
	case UntilChange:
		out := Compare(wt.base, res, 2000)
		wt.base = out.Snapshot
		if out.Kind == Changed {
			return Verdict{Done: true, Reason: ReasonChanged, Detail: out.Diff}, true
		}
	}
	return Verdict{}, false
}

func tail(out string) string {
	if out == "" {
		return ""
	}
	return "; output: " + oneLine(out, 200)
}
