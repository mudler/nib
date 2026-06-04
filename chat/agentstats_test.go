package chat

import (
	"testing"
	"time"
)

func TestHumanTokens(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, ""},
		{-5, ""},
		{1, "1 tokens"},
		{847, "847 tokens"},
		{999, "999 tokens"},
		{1000, "1k tokens"},
		{12000, "12k tokens"},
		{12400, "12.4k tokens"},
		{58800, "58.8k tokens"},
	}
	for _, c := range cases {
		if got := humanTokens(c.in); got != c.want {
			t.Errorf("humanTokens(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHumanDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, ""},
		{12 * time.Second, "12s"},
		{59 * time.Second, "59s"},
		{60 * time.Second, "1m 00s"},
		{63 * time.Second, "1m 03s"},
		{295 * time.Second, "4m 55s"},
		{3661 * time.Second, "1h 01m"},
	}
	for _, c := range cases {
		if got := humanDuration(c.in); got != c.want {
			t.Errorf("humanDuration(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStatsSuffix(t *testing.T) {
	cases := []struct {
		name string
		ev   AgentEvent
		want string
	}{
		{"all", AgentEvent{ToolCount: 3, TotalTokens: 12400, Elapsed: 63 * time.Second}, " · 3 tools · 12.4k tokens · 1m 03s"},
		{"singular tool", AgentEvent{ToolCount: 1, TotalTokens: 500, Elapsed: 5 * time.Second}, " · 1 tool · 500 tokens · 5s"},
		{"no tools", AgentEvent{ToolCount: 0, TotalTokens: 500, Elapsed: 5 * time.Second}, " · 500 tokens · 5s"},
		{"tokens only", AgentEvent{TotalTokens: 500}, " · 500 tokens"},
		{"empty", AgentEvent{}, ""},
	}
	for _, c := range cases {
		if got := c.ev.StatsSuffix(); got != c.want {
			t.Errorf("%s: StatsSuffix() = %q, want %q", c.name, got, c.want)
		}
	}
}
