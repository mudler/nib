package loop

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// Job is a registered recurring (or one-shot) task. Prompt is the payload to
// run when the job fires — a slash command or plain prompt, resolved by the
// host through slash.Resolve at fire time.
type Job struct {
	ID        string    `json:"id"`
	Expr      string    `json:"expr"`
	Prompt    string    `json:"prompt"`
	Recurring bool      `json:"recurring"`
	Durable   bool      `json:"durable"`
	Paused    bool      `json:"paused,omitempty"`
	Created   time.Time `json:"created"`

	// Monitor fields (optional): when set, the job runs a script or fetches a
	// URL at fire time and only dispatches the agent when the output changed
	// (its SHA-256 differs from LastOutputHash). LastOutputHash/LastOutput/
	// LastChangedAt persist across restarts for durable jobs.
	MonitorScript  string    `json:"monitor_script,omitempty"`
	MonitorURL     string    `json:"monitor_url,omitempty"`
	LastOutputHash string    `json:"last_output_hash,omitempty"`
	LastOutput     string    `json:"last_output,omitempty"`
	LastChangedAt  time.Time `json:"last_changed_at,omitempty"`

	sched Schedule  `json:"-"`
	next  time.Time `json:"-"`
}

// MonitorConfig is the monitor mode of a job: at most one of Script or URL is
// set. An empty config means monitor mode is off and the job fires normally.
type MonitorConfig struct {
	Script string
	URL    string
}

// Registry is a thread-safe store of cron jobs with an injectable clock.
type Registry struct {
	mu   sync.Mutex
	jobs []Job
	seq  int
	now  func() time.Time
}

// NewRegistry returns an empty registry using time.Now as its clock.
func NewRegistry() *Registry {
	return &Registry{now: time.Now}
}

// SetClock overrides the clock (tests). Must be called before Add.
func (r *Registry) SetClock(now func() time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = now
}

// Add parses expr, registers a job, and returns it. Returns an error if the
// expression is invalid. monitor (optional) attaches monitor mode: at most one
// of Script/URL may be set; both set returns an error.
func (r *Registry) Add(expr, prompt string, recurring, durable bool, monitor MonitorConfig) (Job, error) {
	if monitor.Script != "" && monitor.URL != "" {
		return Job{}, fmt.Errorf("monitor: set at most one of script or url")
	}
	sched, err := Parse(expr)
	if err != nil {
		return Job{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	now := r.now()
	next, ok := sched.Next(now)
	if !ok {
		return Job{}, fmt.Errorf("cron %q never fires", expr)
	}
	j := Job{
		ID:            fmt.Sprintf("loop-%d", r.seq),
		Expr:          expr,
		Prompt:        prompt,
		Recurring:     recurring,
		Durable:       durable,
		Created:       now,
		MonitorScript: monitor.Script,
		MonitorURL:    monitor.URL,
		sched:         sched,
		next:          next,
	}
	r.jobs = append(r.jobs, j)
	return j, nil
}

// SetMonitorState records the latest monitor output for the job with id and
// returns whether the output changed (the new hash differs from the stored
// one). changedAt is the timestamp to record as the last change time when the
// output changed. Returns false when the job does not exist.
func (r *Registry) SetMonitorState(id, hash, output string, changedAt time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.jobs {
		if r.jobs[i].ID == id {
			if r.jobs[i].LastOutputHash != hash {
				r.jobs[i].LastOutputHash = hash
				r.jobs[i].LastOutput = output
				r.jobs[i].LastChangedAt = changedAt
				return true
			}
			// No change: keep LastOutput/LastChangedAt as-is, only the
			// hash is current; avoid mutating LastChangedAt.
			return false
		}
	}
	return false
}

// List returns a copy of the current jobs, sorted by next-fire time.
func (r *Registry) List() []Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Job, len(r.jobs))
	copy(out, r.jobs)
	sort.Slice(out, func(i, j int) bool { return out[i].next.Before(out[j].next) })
	return out
}

// Delete removes the job with the given id; returns whether it existed.
func (r *Registry) Delete(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, j := range r.jobs {
		if j.ID == id {
			r.jobs = append(r.jobs[:i], r.jobs[i+1:]...)
			return true
		}
	}
	return false
}

// Get returns a copy of the job with the given id.
func (r *Registry) Get(id string) (Job, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, j := range r.jobs {
		if j.ID == id {
			return j, true
		}
	}
	return Job{}, false
}

// Pause stops the job from firing until Resume; it stays registered.
// Returns whether the job exists.
func (r *Registry) Pause(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.jobs {
		if r.jobs[i].ID == id {
			r.jobs[i].Paused = true
			return true
		}
	}
	return false
}

// Resume lets a paused job fire again. Its next fire is recomputed from now,
// so the slots it missed while paused do not fire at once. Returns whether
// the job exists.
func (r *Registry) Resume(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.jobs {
		if r.jobs[i].ID == id {
			if r.jobs[i].Paused {
				r.jobs[i].Paused = false
				if next, ok := r.jobs[i].sched.Next(r.now()); ok {
					r.jobs[i].next = next
				}
			}
			return true
		}
	}
	return false
}

// Due returns the jobs whose next-fire time has arrived (<= now), advancing
// recurring jobs to their next slot and removing fired one-shots. Paused
// jobs are skipped. The registry changes before the caller dispatches, so
// a caller that saves right after Due fires each durable slot at most once,
// even if the process dies mid-dispatch.
func (r *Registry) Due() []Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	var due []Job
	kept := r.jobs[:0]
	for _, j := range r.jobs {
		if j.Paused {
			kept = append(kept, j)
			continue
		}
		if !j.next.After(now) {
			due = append(due, j)
			if j.Recurring {
				if next, ok := j.sched.Next(now); ok {
					j.next = next
					kept = append(kept, j)
				}
			}
			// one-shot (or never-again) → dropped by not appending
		} else {
			kept = append(kept, j)
		}
	}
	r.jobs = kept
	return due
}
