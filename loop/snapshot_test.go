package loop

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestScheduleSnapshotCronLifecycle(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := NewRegistry()
	r.SetClock(func() time.Time { return now })
	j, err := r.Add("* * * * *", "a", true, false, MonitorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	s := r.ScheduleSnapshot()
	if len(s.Entries) != 1 || s.Entries[0].NextDue != now.Add(time.Minute) {
		t.Fatalf("active real next due missing: %+v", s)
	}
	r.Pause(j.ID)
	if len(r.ScheduleSnapshot().Entries) != 0 {
		t.Fatal("paused schedule eligible")
	}
	now = now.Add(5 * time.Minute)
	r.Resume(j.ID)
	s = r.ScheduleSnapshot()
	if s.Entries[0].NextDue != now.Add(time.Minute) {
		t.Fatal("resume used missed slot")
	}
	now = now.Add(time.Minute)
	if len(r.Due()) != 1 {
		t.Fatal("not fired")
	}
	if r.ScheduleSnapshot().Entries[0].NextDue != now.Add(time.Minute) {
		t.Fatal("recurrence not advanced")
	}
	r.Delete(j.ID)
	if len(r.ScheduleSnapshot().Entries) != 0 {
		t.Fatal("deleted schedule eligible")
	}
}

func TestScheduleSnapshotCopiesRevisionsAndLoad(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := NewRegistry()
	r.SetClock(func() time.Time { return now })
	a, _ := r.Add("* * * * *", "active", false, true, MonitorConfig{})
	b, _ := r.Add("* * * * *", "paused", true, true, MonitorConfig{})
	initial := r.ScheduleSnapshot()
	initial.Entries[0].ID = "mutated"
	if r.ScheduleSnapshot().Entries[0].ID != a.ID {
		t.Fatal("snapshot aliases registry")
	}
	rev := r.ScheduleSnapshot().Revision
	r.Pause(b.ID)
	if r.ScheduleSnapshot().Revision <= rev {
		t.Fatal("pause not revisioned")
	}
	path := filepath.Join(t.TempDir(), "loops.json")
	if err := r.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded := NewRegistry()
	now = now.Add(10 * time.Minute)
	loaded.SetClock(func() time.Time { return now })
	if n, err := loaded.Load(path); err != nil || n != 2 {
		t.Fatalf("load: %d %v", n, err)
	}
	s := loaded.ScheduleSnapshot()
	if len(s.Entries) != 1 || !s.Entries[0].NextDue.Equal(now.Add(time.Minute)) || s.Revision == 0 {
		t.Fatalf("load snapshot: %+v", s)
	}
	now = now.Add(59 * time.Second)
	if len(loaded.Due()) != 0 {
		t.Fatal("early fire")
	}
	now = now.Add(time.Second)
	if len(loaded.Due()) != 1 || len(loaded.ScheduleSnapshot().Entries) != 0 {
		t.Fatal("one shot not removed")
	}
	if len(loaded.Due()) != 0 {
		t.Fatal("repeated dispatch")
	}
	if len(initial.Entries) != 2 || initial.Entries[1].NextDue != time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC) {
		t.Fatal("snapshot mutated after publication")
	}
}
func TestScheduleSnapshotConcurrentPublication(t *testing.T) {
	r := NewRegistry()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r.SetClock(func() time.Time { return now })
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				j, err := r.Add("* * * * *", "a", true, false, MonitorConfig{})
				if err != nil {
					t.Error(err)
					return
				}
				r.Pause(j.ID)
				r.Resume(j.ID)
				r.ScheduleSnapshot()
				r.Delete(j.ID)
			}
		}()
	}
	wg.Wait()
	if len(r.ScheduleSnapshot().Entries) != 0 {
		t.Fatal("deleted entries remain")
	}
}
