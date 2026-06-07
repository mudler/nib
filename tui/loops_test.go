package tui

import (
	"testing"
	"time"

	"github.com/mudler/nib/loop"
)

func TestDurationToCron(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "* * * * *"}, // sub-minute → every minute (floor)
		{5 * time.Minute, "*/5 * * * *"},
		{1 * time.Hour, "0 */1 * * *"},
		{90 * time.Minute, "0 * * * *"}, // ≥1h non-aligned → hourly (minute step can't exceed 59)
	}
	for _, c := range cases {
		if got := durationToCron(c.d); got != c.want {
			t.Fatalf("durationToCron(%s) = %q want %q", c.d, got, c.want)
		}
	}
}

func TestRenderLoopsFooter(t *testing.T) {
	r := loop.NewRegistry()
	if f := renderLoopsFooter(r, 0, 80); f != "" {
		t.Fatalf("empty registry should render nothing, got %q", f)
	}
	r.Add("*/5 * * * *", "/foo", true, false)
	if f := renderLoopsFooter(r, 0, 80); f == "" {
		t.Fatal("expected non-empty footer with one job")
	}
}
