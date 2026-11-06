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
)

func (m Mode) String() string {
	switch m {
	case UntilChange:
		return "change"
	case UntilMatch:
		return "match"
	case UntilExitZero:
		return "exit-zero"
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
	// Timeout ends the wait. It must be positive.
	Timeout time.Duration
	// MaxErrors is how many failed checks in a row end the wait. Zero means
	// DefaultMaxErrors.
	MaxErrors int
}

// Reason says why a wait ended.
type Reason string

const (
	ReasonChanged       Reason = "changed"
	ReasonMatched       Reason = "matched"
	ReasonExitedZero    Reason = "exited-zero"
	ReasonSourceFailing Reason = "source-failing"
	ReasonTimedOut      Reason = "timed-out"
)

// Satisfied reports whether the condition was met, as opposed to a failure or
// a timeout.
func (r Reason) Satisfied() bool {
	return r == ReasonChanged || r == ReasonMatched || r == ReasonExitedZero
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
}

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

	switch wt.w.Mode {
	case UntilExitZero:
		if res.Err == nil {
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
