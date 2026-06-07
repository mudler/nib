// Package loop provides a zero-dependency cron parser and an injectable-clock
// job registry backing nib's /loop feature.
package loop

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed cron expression: [second] minute hour day-of-month month
// day-of-week, all evaluated in the timezone of the time passed to Next. The
// leading seconds field is optional; when absent it defaults to second 0.
type Schedule struct {
	sec                      uint64 // bitmask, seconds (0-59); defaults to second 0
	min, hour, dom, mon, dow uint64 // bitmask per field
}

// field bounds for the 5 clock fields min/hour/dom/mon/dow: {min,max} inclusive.
var bounds = [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}

// Parse parses a 5- or 6-field cron expression. With 6 fields the leading field
// is seconds (0-59); with 5 fields seconds defaults to 0 (backward compatible).
// Supports '*', '*/n', 'a-b', 'a,b,c', and single values per field.
// Day-of-week 0 = Sunday.
//
// All fields are ANDed together — including day-of-month and day-of-week.
// This differs from POSIX/Vixie cron, which ORs day-of-month and day-of-week
// when both are restricted. The deviation is acceptable here because nib's
// /loop never restricts both fields at once.
func Parse(expr string) (Schedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 && len(fields) != 6 {
		return Schedule{}, fmt.Errorf("cron: want 5 or 6 fields, got %d in %q", len(fields), expr)
	}
	var s Schedule
	if len(fields) == 6 {
		mask, err := parseField(fields[0], 0, 59)
		if err != nil {
			return Schedule{}, fmt.Errorf("cron field 0 (%q): %w", fields[0], err)
		}
		s.sec = mask
		fields = fields[1:]
	} else {
		s.sec = 1 << 0 // second 0 only
	}
	dst := []*uint64{&s.min, &s.hour, &s.dom, &s.mon, &s.dow}
	for i, f := range fields {
		mask, err := parseField(f, bounds[i][0], bounds[i][1])
		if err != nil {
			return Schedule{}, fmt.Errorf("cron field %d (%q): %w", i, f, err)
		}
		*dst[i] = mask
	}
	return s, nil
}

func parseField(f string, lo, hi int) (uint64, error) {
	var mask uint64
	for _, part := range strings.Split(f, ",") {
		step := 1
		rng := part
		if i := strings.Index(part, "/"); i >= 0 {
			rng = part[:i]
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("bad step %q", part)
			}
			step = n
		}
		start, end := lo, hi
		switch {
		case rng == "*":
			// full range
		case strings.Contains(rng, "-"):
			ab := strings.SplitN(rng, "-", 2)
			a, err1 := strconv.Atoi(ab[0])
			b, err2 := strconv.Atoi(ab[1])
			if err1 != nil || err2 != nil || a > b {
				return 0, fmt.Errorf("bad range %q", rng)
			}
			start, end = a, b
		default:
			v, err := strconv.Atoi(rng)
			if err != nil {
				return 0, fmt.Errorf("bad value %q", rng)
			}
			start, end = v, v
		}
		if start < lo || end > hi {
			return 0, fmt.Errorf("out of bounds %q", part)
		}
		for v := start; v <= end; v += step {
			mask |= 1 << uint(v)
		}
	}
	if mask == 0 {
		return 0, fmt.Errorf("empty field")
	}
	return mask, nil
}

// matchClock reports whether t matches every field except seconds.
func (s Schedule) matchClock(t time.Time) bool {
	return s.min&(1<<uint(t.Minute())) != 0 &&
		s.hour&(1<<uint(t.Hour())) != 0 &&
		s.dom&(1<<uint(t.Day())) != 0 &&
		s.mon&(1<<uint(int(t.Month()))) != 0 &&
		s.dow&(1<<uint(int(t.Weekday()))) != 0
}

func (s Schedule) match(t time.Time) bool {
	return s.sec&(1<<uint(t.Second())) != 0 && s.matchClock(t)
}

// Next returns the first instant strictly after `after` that matches the
// schedule, at second granularity. ok is false if none is found within ~4
// years (guards bad exprs).
func (s Schedule) Next(after time.Time) (time.Time, bool) {
	t := after.Truncate(time.Second).Add(time.Second)
	limit := t.AddDate(4, 0, 0)
	for t.Before(limit) {
		// Match minute/hour/dom/mon/dow first (ignore seconds here).
		if !s.matchClock(t) {
			t = t.Add(time.Minute).Truncate(time.Minute) // next minute, sec 0
			continue
		}
		// Minute matches: smallest matching second >= t.Second().
		for v := t.Second(); v <= 59; v++ {
			if s.sec&(1<<uint(v)) != 0 {
				return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), v, 0, t.Location()), true
			}
		}
		t = t.Add(time.Minute).Truncate(time.Minute) // no second this minute → next minute
	}
	return time.Time{}, false
}
