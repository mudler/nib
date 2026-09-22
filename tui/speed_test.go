package tui

import (
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/mudler/nib/chat"
)

// feed records n one-token chunks, one every gap, from start; it returns the
// time of the last one.
func feed(s *speedMeter, start time.Time, n int, gap time.Duration) time.Time {
	at := start
	for i := 0; i < n; i++ {
		at = start.Add(time.Duration(i) * gap)
		s.record(3, at) // under 4 bytes: one token each
	}
	return at
}

func near(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > want*0.05 {
		t.Errorf("%s = %.2f, want about %.2f", name, got, want)
	}
}

// While chunks arrive, the reading is live at the rate they arrive.
func TestSpeedLiveRate(t *testing.T) {
	var s speedMeter
	t0 := time.Now()
	last := feed(&s, t0, 101, 20*time.Millisecond) // 50 tok/s for 2s
	r, ok := s.read(last)
	if !ok || !r.Live {
		t.Fatalf("reading = %+v, ok %v; want live", r, ok)
	}
	near(t, "live rate", r.Rate, 50)
	near(t, "average", r.Avg, 50)
	if len(r.Spark) != speedBuckets {
		t.Fatalf("spark has %d bars, want %d", len(r.Spark), speedBuckets)
	}
}

// A pause longer than speedGap (a tool call, the next request waiting for
// its first token) is not generation time: the average stays the decode
// speed, and idle the reading shows the last stretch's rate.
func TestSpeedLeavesPausesOut(t *testing.T) {
	var s speedMeter
	t0 := time.Now()
	end1 := feed(&s, t0, 51, 20*time.Millisecond) // 50 tok/s for 1s
	start2 := end1.Add(10 * time.Second)
	end2 := feed(&s, start2, 26, 40*time.Millisecond) // 25 tok/s for 1s

	r, _ := s.read(end2.Add(5 * time.Second))
	if r.Live {
		t.Fatal("reading still live 5s after the last chunk")
	}
	near(t, "last rate", r.Rate, 25)
	// 77 tokens over 2s of generation, not over the 12s of wall time.
	near(t, "average", r.Avg, 77.0/2)
}

// A chunk of many bytes counts as bytes/4 tokens, so a provider that sends
// several tokens per chunk is not measured as one token per chunk.
func TestSpeedCountsLongChunksByBytes(t *testing.T) {
	var s speedMeter
	t0 := time.Now()
	for i := 0; i <= 10; i++ {
		s.record(40, t0.Add(time.Duration(i)*100*time.Millisecond)) // 10 tokens each
	}
	r, _ := s.read(t0.Add(time.Second))
	near(t, "average", r.Avg, 110)
}

func TestSpeedBadges(t *testing.T) {
	m := Model{speed: &speedMeter{}, loading: true}
	if full, narrow := m.speedBadges(); full != "" || narrow != "" || m.liveSpeed() != "" {
		t.Fatal("speed shown before any chunk")
	}
	feed(m.speed, time.Now().Add(-time.Second), 51, 20*time.Millisecond)
	full, narrow := m.speedBadges()
	if got := ansi.Strip(full); !strings.HasPrefix(got, "tok/s ") || !strings.Contains(got, " avg ") {
		t.Fatalf("full badge = %q, want the rate and the average", got)
	}
	if got := ansi.Strip(narrow); !strings.HasPrefix(got, "avg ") || !strings.HasSuffix(got, " tok/s") {
		t.Fatalf("narrow badge = %q, want the average alone", got)
	}
	if got := ansi.Strip(m.liveSpeed()); !strings.Contains(got, " tok/s") {
		t.Fatalf("live speed = %q, want the live rate", got)
	}
	m.loading = false
	if got := m.liveSpeed(); got != "" {
		t.Fatalf("live speed shown with no turn running: %q", got)
	}
}

func TestFormatRate(t *testing.T) {
	for r, want := range map[float64]string{0.46: "0.5", 7.25: "7.2", 42.4: "42", 131.6: "132"} {
		if got := formatRate(r); got != want {
			t.Errorf("formatRate(%v) = %q, want %q", r, got, want)
		}
	}
}

// The working indicator counts the tokens of the turn in progress, keeps the
// count while a tool runs, and starts over with the next turn.
func TestLiveSpeedTurnTokens(t *testing.T) {
	var gen atomic.Int32
	m := Model{speed: &speedMeter{}, loading: true, turnGen: &gen}
	t0 := time.Now().Add(-time.Second)
	for i := 0; i < 50; i++ {
		m.speed.recordTurn(1, 3, t0.Add(time.Duration(i)*20*time.Millisecond))
	}
	gen.Store(1)
	if got := ansi.Strip(m.liveSpeed()); !strings.Contains(got, " tok/s") || !strings.HasSuffix(got, "50 tokens") {
		t.Fatalf("live speed = %q, want the rate and 50 tokens", got)
	}

	// A tool runs: no chunk for longer than speedGap. The rate goes, the
	// count stays.
	m.speed.mu.Lock()
	m.speed.start = m.speed.start.Add(-10 * time.Second)
	m.speed.last = m.speed.last.Add(-10 * time.Second)
	m.speed.mu.Unlock()
	if got := ansi.Strip(m.liveSpeed()); got != "50 tokens" {
		t.Fatalf("live speed while a tool runs = %q, want %q", got, "50 tokens")
	}

	// The next turn has no chunk yet: the last turn's count is not shown.
	gen.Store(2)
	if got := m.liveSpeed(); got != "" {
		t.Fatalf("live speed before the new turn's first chunk = %q, want empty", got)
	}
	m.speed.recordTurn(2, 3, time.Now())
	if got := ansi.Strip(m.liveSpeed()); !strings.HasSuffix(got, "1 tokens") {
		t.Fatalf("live speed = %q, want the new turn's count", got)
	}
}

// Each sub-agent's stream is metered on its own, and its landing line gets
// the rate, and the streamed count when the backend reported no usage.
func TestAgentStreamStats(t *testing.T) {
	m := Model{agentSpeed: &agentMeters{}}
	t0 := time.Now().Add(-2 * time.Second)
	for i := 0; i <= 50; i++ {
		m.agentSpeed.record("a1", 3, t0.Add(time.Duration(i)*20*time.Millisecond))
	}
	m.agentSpeed.record("", 3, t0) // the main agent's: not metered here

	ev := m.withStreamStats(chat.AgentEvent{ID: "a1", Status: chat.AgentStatusCompleted, TotalTokens: 900})
	near(t, "rate", ev.TokensPerSec, 51)
	if ev.OutputTokens != 51 || !ev.OutputEstimated {
		t.Fatalf("output = %d (estimated %v), want the streamed 51, estimated", ev.OutputTokens, ev.OutputEstimated)
	}
	if got := agentTranscriptLine(ev); !strings.Contains(got, "900 tokens (~51 out) · 51 tok/s") {
		t.Fatalf("landing line = %q", got)
	}
	if _, _, ok := m.agentSpeed.finish("a1", time.Now()); ok {
		t.Fatal("meter kept after the agent landed")
	}

	// Measured output wins over the streamed count.
	m.agentSpeed.record("a2", 3, t0)
	m.agentSpeed.record("a2", 3, t0.Add(time.Second))
	ev = m.withStreamStats(chat.AgentEvent{ID: "a2", Status: chat.AgentStatusCompleted, OutputTokens: 7})
	if ev.OutputTokens != 7 || ev.OutputEstimated {
		t.Fatalf("output = %d (estimated %v), want the measured 7", ev.OutputTokens, ev.OutputEstimated)
	}
}
