package loop

import "time"

type ScheduleSnapshot struct {
	Revision uint64
	Entries  []ScheduleEntry
}
type ScheduleEntry struct {
	ID      string
	Active  bool
	NextDue time.Time
}

func (r *Registry) ScheduleSnapshot() ScheduleSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := ScheduleSnapshot{Revision: r.revision}
	for _, j := range r.jobs {
		if !j.Paused && !j.next.IsZero() {
			s.Entries = append(s.Entries, ScheduleEntry{ID: j.ID, Active: true, NextDue: j.next})
		}
	}
	return s
}
