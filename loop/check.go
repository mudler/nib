package loop

import (
	"fmt"
	"strings"
	"time"

	"github.com/mudler/nib/watch"
)

// MaxDiff caps the diff put in the prompt of a monitor job that saw a change.
const MaxDiff = 4000

// errorReportEvery is how often a monitor that keeps failing reports it.
const errorReportEvery = 10

// isCheck reports whether the job checks a source at fire time instead of
// running its prompt at once: a monitor job or a wait.
func (j Job) isCheck() bool {
	return j.MonitorScript != "" || j.MonitorURL != "" || j.wait != nil
}

// IsCheck reports whether the job checks a source when it fires (a monitor job
// or a wait made by AddWait). A host runs the check with Source and hands the
// result to Registry.Observe, instead of dispatching the prompt.
func (j Job) IsCheck() bool { return j.isCheck() }

// Source is the source a checking job reads. It is empty for a plain job.
func (j Job) Source() watch.Source {
	if j.wait != nil {
		return j.wait.Wait().Source
	}
	return watch.Source{Script: j.MonitorScript, URL: j.MonitorURL}
}

// Label describes what a checking job does, for lists and the footer. It is
// empty for a plain job.
func (j Job) Label() string {
	switch {
	case j.wait != nil:
		w := j.wait.Wait()
		return fmt.Sprintf("wait_for %s on %s until %s", w.Mode, w.Source, j.wait.Deadline().Format("15:04:05"))
	case j.isCheck():
		l := "monitor " + j.Source().String()
		if j.LastError != "" {
			l += " (last check failed: " + oneLine(j.LastError) + ")"
		}
		return l
	}
	return ""
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}

// AddWait registers a wait: a job that checks w.Source each time expr fires
// and ends, with a wake-up for the model, when the condition is met, the source
// keeps failing, or the timeout passes. then is what to tell the model to do
// next. A wait lives only in memory.
func (r *Registry) AddWait(expr string, w watch.Wait, then string) (Job, error) {
	sched, err := Parse(expr)
	if err != nil {
		return Job{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	wt, err := watch.NewWaiter(w, now)
	if err != nil {
		return Job{}, err
	}
	next, ok := sched.Next(now)
	if !ok {
		return Job{}, fmt.Errorf("cron %q never fires", expr)
	}
	r.seq++
	j := Job{
		ID:        fmt.Sprintf("loop-%d", r.seq),
		Expr:      expr,
		Prompt:    then,
		Recurring: true,
		Created:   now,
		sched:     sched,
		next:      next,
		wait:      wt,
	}
	r.jobs = append(r.jobs, j)
	r.revision++
	return j, nil
}

// BeginCheck claims the job id for one check and returns a copy of it. It
// returns false when the job is gone, is not a checking job, or its previous
// check has not finished: a slow source then skips a tick instead of piling up
// checks. Every successful BeginCheck must be followed by Observe.
func (r *Registry) BeginCheck(id string) (Job, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.jobs {
		if r.jobs[i].ID == id {
			if !r.jobs[i].isCheck() || r.jobs[i].busy {
				return Job{}, false
			}
			r.jobs[i].busy = true
			return r.jobs[i], true
		}
	}
	return Job{}, false
}

// Decision is what a host does after a check.
type Decision struct {
	// Dispatch is true when the model must be woken with Prompt.
	Dispatch bool
	Prompt   string
	// Err is a failed check worth showing to the user. A source that keeps
	// failing is reported on the first failure and then every few.
	Err error
	// Persist is true when the stored state of a durable job changed: save the
	// registry. Do it before you dispatch.
	Persist bool
}

// Observe records the result of a check started with BeginCheck and says what
// to do. The new state is stored before it returns, so a host that saves and
// then dispatches never reports the same change twice, even if the dispatched
// run fails.
//
// For a monitor job: a failed check is an error, never a change, and leaves the
// stored hash alone; the first successful check stores a baseline and does not
// dispatch unless MonitorNotifyFirst is set; a changed hash dispatches the job
// prompt with a capped diff. For a wait: it dispatches once, when the wait
// ends, with the reason. A one-shot monitor and every ended wait are removed.
func (r *Registry) Observe(id string, res watch.Result, now time.Time) Decision {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := -1
	for i := range r.jobs {
		if r.jobs[i].ID == id {
			idx = i
		}
	}
	if idx < 0 {
		return Decision{} // deleted while the check ran
	}
	j := &r.jobs[idx]
	j.busy = false
	if res.Err != nil {
		j.LastError = res.Err.Error()
	} else {
		j.LastError, j.ErrorStreak = "", 0
	}

	if j.wait != nil {
		v := j.wait.Step(res, now)
		if !v.Done {
			return Decision{}
		}
		prompt := waitPrompt(*j, v)
		r.removeLocked(idx)
		return Decision{Dispatch: true, Prompt: prompt}
	}

	out := watch.Compare(watch.Snapshot{Hash: j.LastOutputHash, Output: j.LastOutput}, res, MaxDiff)
	var d Decision
	switch out.Kind {
	case watch.Failed:
		j.ErrorStreak++
		if j.ErrorStreak == 1 || j.ErrorStreak%errorReportEvery == 0 {
			d.Err = fmt.Errorf("monitor %s: %s failed (%d in a row): %w", j.ID, j.Source(), j.ErrorStreak, out.Err)
		}
		return d
	case watch.Unchanged:
		return d
	}
	j.LastOutputHash, j.LastOutput, j.LastChangedAt = out.Snapshot.Hash, out.Snapshot.Output, now
	d.Persist = j.Durable
	switch {
	case out.Kind == watch.Changed:
		d.Dispatch, d.Prompt = true, monitorPrompt(*j, "changed since the last check", "diff", out.Diff)
	case j.MonitorNotifyFirst:
		d.Dispatch, d.Prompt = true, monitorPrompt(*j, "first check", "", out.Snapshot.Output)
	}
	if d.Dispatch && !j.Recurring {
		r.removeLocked(idx) // j is stale from here
	}
	return d
}

func (r *Registry) removeLocked(i int) {
	r.jobs = append(r.jobs[:i], r.jobs[i+1:]...)
	r.revision++
}

func monitorPrompt(j Job, what, lang, body string) string {
	var b strings.Builder
	if j.Prompt != "" {
		b.WriteString(j.Prompt + "\n\n")
	}
	fmt.Fprintf(&b, "[monitor %s: %s %s]\n", j.ID, j.Source(), what)
	if body != "" {
		fmt.Fprintf(&b, "```%s\n%s", lang, body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n")
	}
	return b.String()
}

func waitPrompt(j Job, v watch.Verdict) string {
	w := j.wait.Wait()
	var b strings.Builder
	if v.Reason.Satisfied() {
		fmt.Fprintf(&b, "[wait_for %s ended: the condition was met (%s) on %s.]\n", j.ID, v.Reason, w.Source)
	} else {
		fmt.Fprintf(&b, "[wait_for %s ended WITHOUT the condition being met (%s) on %s. Do not treat this as success.]\n", j.ID, v.Reason, w.Source)
	}
	if v.Detail != "" {
		b.WriteString(v.Detail)
		if !strings.HasSuffix(v.Detail, "\n") {
			b.WriteString("\n")
		}
	}
	if j.Prompt != "" {
		b.WriteString("\nNext: " + j.Prompt + "\n")
	}
	return b.String()
}

// Now is the registry clock. Pass it to Observe as the time of a check, so a
// host that sets a clock sees one time everywhere.
func (r *Registry) Now() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now()
}
