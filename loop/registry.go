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
	Created   time.Time `json:"created"`

	sched Schedule  `json:"-"`
	next  time.Time `json:"-"`
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
// expression is invalid.
func (r *Registry) Add(expr, prompt string, recurring, durable bool) (Job, error) {
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
		ID:        fmt.Sprintf("loop-%d", r.seq),
		Expr:      expr,
		Prompt:    prompt,
		Recurring: recurring,
		Durable:   durable,
		Created:   now,
		sched:     sched,
		next:      next,
	}
	r.jobs = append(r.jobs, j)
	return j, nil
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

// Due returns the jobs whose next-fire time has arrived (<= now), advancing
// recurring jobs to their next slot and removing fired one-shots.
func (r *Registry) Due() []Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	var due []Job
	kept := r.jobs[:0]
	for _, j := range r.jobs {
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
