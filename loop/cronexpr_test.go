package loop

import (
	"testing"
	"time"
)

func tm(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParseInvalid(t *testing.T) {
	for _, expr := range []string{"", "* * *", "60 * * * *", "* 24 * * *", "*/0 * * * *", "a * * * *", "5- * * * *"} {
		if _, err := Parse(expr); err == nil {
			t.Fatalf("expected error for %q", expr)
		}
	}
}

func TestNext(t *testing.T) {
	cases := []struct {
		expr  string
		after string
		want  string
	}{
		{"*/5 * * * *", "2026-06-06 10:02", "2026-06-06 10:05"},
		{"*/5 * * * *", "2026-06-06 10:05", "2026-06-06 10:10"}, // strictly after
		{"0 * * * *", "2026-06-06 10:30", "2026-06-06 11:00"},
		{"30 14 * * *", "2026-06-06 10:00", "2026-06-06 14:30"},
		{"0 9 * * 1-5", "2026-06-06 10:00", "2026-06-08 09:00"}, // Sat 6th → Mon 8th
		{"15,45 * * * *", "2026-06-06 10:20", "2026-06-06 10:45"},
		{"0 0 1 1 *", "2026-06-06 10:00", "2027-01-01 00:00"},
	}
	for _, c := range cases {
		s, err := Parse(c.expr)
		if err != nil {
			t.Fatalf("parse %q: %v", c.expr, err)
		}
		got, ok := s.Next(tm(c.after))
		if !ok {
			t.Fatalf("%q: no next fire", c.expr)
		}
		if !got.Equal(tm(c.want)) {
			t.Fatalf("%q after %s: got %s want %s", c.expr, c.after, got.Format("2006-01-02 15:04"), c.want)
		}
	}
}
