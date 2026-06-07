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

func tms(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParseSixField(t *testing.T) {
	// 6 fields: leading seconds. Valid.
	if _, err := Parse("*/5 * * * * *"); err != nil {
		t.Fatalf("6-field should parse: %v", err)
	}
	// 4 or 7 fields invalid.
	for _, expr := range []string{"* * * *", "* * * * * * *", "60 * * * * *"} {
		if _, err := Parse(expr); err == nil {
			t.Fatalf("expected error for %q", expr)
		}
	}
}

func TestNextSeconds(t *testing.T) {
	cases := []struct {
		expr  string
		after string
		want  string
	}{
		{"*/5 * * * * *", "2026-06-06 10:00:02", "2026-06-06 10:00:05"},
		{"*/5 * * * * *", "2026-06-06 10:00:05", "2026-06-06 10:00:10"}, // strictly after
		{"*/5 * * * * *", "2026-06-06 10:00:57", "2026-06-06 10:01:00"}, // rolls to next minute
		{"*/10 * * * * *", "2026-06-06 10:00:00", "2026-06-06 10:00:10"},
		{"30 * * * * *", "2026-06-06 10:00:00", "2026-06-06 10:00:30"}, // sec=30
		{"15,45 * * * * *", "2026-06-06 10:00:20", "2026-06-06 10:00:45"},
	}
	for _, c := range cases {
		s, err := Parse(c.expr)
		if err != nil {
			t.Fatalf("parse %q: %v", c.expr, err)
		}
		got, ok := s.Next(tms(c.after))
		if !ok {
			t.Fatalf("%q: no next fire", c.expr)
		}
		if !got.Equal(tms(c.want)) {
			t.Fatalf("%q after %s: got %s want %s", c.expr, c.after, got.Format("15:04:05"), c.want)
		}
	}
}

// A 5-field expr fires at second 0 (backward compatible).
func TestFiveFieldSecondZero(t *testing.T) {
	s, err := Parse("*/5 * * * *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, ok := s.Next(tms("2026-06-06 10:04:30"))
	if !ok {
		t.Fatal("no next")
	}
	if !got.Equal(tms("2026-06-06 10:05:00")) {
		t.Fatalf("5-field should fire at sec 0: got %s", got.Format("15:04:05"))
	}
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
