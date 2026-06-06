// Package loop provides a zero-dependency 5-field cron parser and an
// injectable-clock job registry backing nib's /loop feature.
package loop

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed 5-field cron expression: minute hour day-of-month month
// day-of-week, all in local time.
type Schedule struct {
	min, hour, dom, mon, dow uint64 // bitmask per field
}

// field bounds: {min,max} inclusive.
var bounds = [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}

// Parse parses a standard 5-field cron expression. Supports '*', '*/n', 'a-b',
// 'a,b,c', and single values per field. Day-of-week 0 = Sunday.
func Parse(expr string) (Schedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return Schedule{}, fmt.Errorf("cron: want 5 fields, got %d in %q", len(fields), expr)
	}
	var s Schedule
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

func (s Schedule) match(t time.Time) bool {
	return s.min&(1<<uint(t.Minute())) != 0 &&
		s.hour&(1<<uint(t.Hour())) != 0 &&
		s.dom&(1<<uint(t.Day())) != 0 &&
		s.mon&(1<<uint(int(t.Month()))) != 0 &&
		s.dow&(1<<uint(int(t.Weekday()))) != 0
}

// Next returns the first minute strictly after `after` that matches the
// schedule. ok is false if none is found within ~4 years (guards bad exprs).
func (s Schedule) Next(after time.Time) (time.Time, bool) {
	// Truncate to the minute and step forward minute-by-minute.
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(4, 0, 0)
	for ; t.Before(limit); t = t.Add(time.Minute) {
		if s.match(t) {
			return t, true
		}
	}
	return time.Time{}, false
}
