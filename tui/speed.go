package tui

import (
	"math"
	"strings"
	"sync"
	"time"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/theme"
)

// speedMeter measures how fast the model generates, from the stream itself.
//
// It cannot use measured usage: cogito's bundled clients never populate
// StreamEvent.Usage, so a streamed turn reports no completion tokens (see
// chat/usage.go). Each streamed chunk (reasoning or content) is counted as
// max(1, bytes/4) tokens instead. Most local servers send one token per
// chunk, which the 1 counts exactly; a provider that sends several tokens per
// chunk is caught by the bytes/4 estimate the context badge also uses.
//
// Only generation time counts. A stretch runs from a chunk to the next one
// while they come less than speedGap apart, so waiting for the first token
// (the prompt being read) and running tools are left out: the figure is the
// model's decode speed, which is what changes between models and machines.
//
// OnStream records on the session's goroutine and the footer reads on the UI
// goroutine, so the meter has its own lock.
type speedMeter struct {
	mu sync.Mutex

	// The stretch in progress: from its first chunk (start) to its latest
	// (last), with tokens generated in it.
	active      bool
	start, last time.Time
	tokens      float64

	// Stretches that have ended, for the session average.
	doneTokens float64
	doneTime   time.Duration
	// lastRate is the rate of the stretch that ended last, shown when the
	// model is idle.
	lastRate float64

	// buckets hold tokens per speedBucket, the newest at the end, for the
	// live rate and the sparkline. bucketAt is when the newest one started.
	buckets  [speedBuckets]float64
	bucketAt time.Time

	// turn is the turn generation (Model.turnGen) the chunks of turnTokens
	// came in, and turnTokens the tokens generated in it: the count the
	// working indicator shows. total counts every chunk.
	turn       int32
	turnTokens float64
	total      float64
}

const (
	// speedGap ends a stretch: a pause this long is a tool call or a new
	// request, not the model generating.
	speedGap = 2 * time.Second
	// speedBucket and speedBuckets size the live window: the live rate is
	// over the last two seconds, and the sparkline shows each bucket.
	speedBucket  = 250 * time.Millisecond
	speedBuckets = 8
)

// record counts one streamed chunk of n bytes, received at now.
func (s *speedMeter) record(n int, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordLocked(n, now)
}

// recordTurn counts one streamed chunk of n bytes, received at now in the
// turn of generation gen. The turn count starts over when gen changes.
func (s *speedMeter) recordTurn(gen int32, n int, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.turn {
		s.turn, s.turnTokens = gen, 0
	}
	s.recordLocked(n, now)
}

func (s *speedMeter) recordLocked(n int, now time.Time) {
	if n <= 0 {
		return
	}
	tokens := math.Max(1, float64(n)/4)
	s.turnTokens += tokens
	s.total += tokens
	if s.active && now.Sub(s.last) > speedGap {
		s.closeLocked()
	}
	if !s.active {
		s.active, s.start, s.tokens = true, now, 0
		s.buckets, s.bucketAt = [speedBuckets]float64{}, now
	}
	s.last = now
	s.tokens += tokens
	s.shiftLocked(now)
	s.buckets[speedBuckets-1] += tokens
}

// closeLocked ends the stretch in progress and adds it to the session.
func (s *speedMeter) closeLocked() {
	if d := s.last.Sub(s.start); d > 0 {
		s.doneTokens += s.tokens
		s.doneTime += d
		s.lastRate = s.stretchRate()
	}
	s.active = false
}

// stretchRate is the decode rate of the stretch in progress. Its first
// token arrives at start, so it does not count toward the time after it.
func (s *speedMeter) stretchRate() float64 {
	d := s.last.Sub(s.start).Seconds()
	if d <= 0 {
		return 0
	}
	return math.Max(0, s.tokens-1) / d
}

// shiftLocked moves the bucket window forward so its newest bucket holds now.
func (s *speedMeter) shiftLocked(now time.Time) {
	steps := int(now.Sub(s.bucketAt) / speedBucket)
	if steps <= 0 {
		return
	}
	if steps >= speedBuckets {
		s.buckets = [speedBuckets]float64{}
	} else {
		copy(s.buckets[:], s.buckets[steps:])
		for i := speedBuckets - steps; i < speedBuckets; i++ {
			s.buckets[i] = 0
		}
	}
	s.bucketAt = s.bucketAt.Add(time.Duration(steps) * speedBucket)
}

// speedReading is what the footer shows.
type speedReading struct {
	// Turn is the generation of the turn the latest chunk came in, and
	// TurnTokens the tokens generated in that turn. Total counts every chunk.
	Turn       int32
	TurnTokens float64
	Total      float64

	// Live is true while the model is generating: Rate is then the rate over
	// the last two seconds and Spark the tokens per second of each bucket.
	// Idle, Rate is the rate of the last stretch.
	Live  bool
	Rate  float64
	Avg   float64
	Spark []float64
}

// read returns the meter's reading at now; ok is false before any chunk.
func (s *speedMeter) read(now time.Time) (r speedReading, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active && now.Sub(s.last) > speedGap {
		s.closeLocked()
	}
	tokens, dur := s.doneTokens, s.doneTime
	if s.active {
		tokens += s.tokens
		dur += s.last.Sub(s.start)
	}
	if tokens == 0 {
		return r, false
	}
	r.Turn, r.TurnTokens, r.Total = s.turn, s.turnTokens, s.total
	if dur > 0 {
		r.Avg = tokens / dur.Seconds()
	}
	if !s.active {
		r.Rate = s.lastRate
		return r, true
	}
	r.Live = true
	s.shiftLocked(now)
	// The buckets cover the seven before the newest one plus the part of the
	// newest that has passed. And the window cannot be longer than the
	// stretch: a stretch 500ms old has half a second of tokens, not two.
	covered := now.Sub(s.bucketAt) + (speedBuckets-1)*speedBucket
	window := min(covered, now.Sub(s.start))
	var sum float64
	for _, b := range s.buckets {
		sum += b
	}
	if window > 0 {
		r.Rate = sum / window.Seconds()
	} else {
		r.Rate = s.stretchRate()
	}
	r.Spark = make([]float64, speedBuckets)
	for i, b := range s.buckets {
		r.Spark[i] = b / speedBucket.Seconds()
	}
	return r, true
}

// liveSpeed renders the rate and the tokens generated this turn for the
// working indicator line, "42 tok/s ▃▅▆▇ · 1.2k tokens", while the model
// works. The rate shows only while the model is generating; the count stays
// while a tool runs. "" before the turn's first chunk. It sits where the user
// looks while the model works, and needs no room in the footer.
func (m Model) liveSpeed() string {
	if m.speed == nil || !m.loading {
		return ""
	}
	r, ok := m.speed.read(time.Now())
	if !ok {
		return ""
	}
	var parts []string
	if r.Live {
		rate := theme.Running.Render(formatRate(r.Rate)) + theme.Help.Render(" tok/s")
		if spark := theme.Sparkline(r.Spark); spark != "" {
			rate += " " + theme.Running.Render(spark)
		}
		parts = append(parts, rate)
	}
	// The count is of this turn's chunks: one from a turn before the first
	// chunk of this one would show the last turn's count.
	if r.Turn == m.currentTurnGen() && r.TurnTokens > 0 {
		n := chat.HumanTokens(int(math.Round(r.TurnTokens)))
		parts = append(parts, theme.Meta.Render(n)+theme.Help.Render(" tokens"))
	}
	return strings.Join(parts, " "+theme.SepStyle.Render(theme.Sep)+" ")
}

// speedBadges renders the footer's speed badge in its full form, "tok/s 41 ·
// avg 38" (the rate now, or of the last reply when idle, and the session
// average), and its narrow form, "avg 38 tok/s". Both are "" before the model
// has generated anything.
func (m Model) speedBadges() (full, narrow string) {
	if m.speed == nil {
		return "", ""
	}
	r, ok := m.speed.read(time.Now())
	if !ok || r.Avg <= 0 {
		return "", ""
	}
	avg := theme.Meta.Render(formatRate(r.Avg))
	full = theme.Help.Render("tok/s ") + theme.Meta.Render(formatRate(r.Rate)) +
		theme.Help.Render(" "+theme.Sep+" avg ") + avg
	narrow = theme.Help.Render("avg ") + avg + theme.Help.Render(" tok/s")
	return full, narrow
}

// formatRate formats a rate in tokens per second (see chat.HumanRate).
func formatRate(r float64) string { return chat.HumanRate(r) }

// agentMeters meters each sub-agent's stream on its own, for the rate and the
// output count its landing line reports. OnStream records into it from the
// session's goroutine and Update reads it, so it has its own lock.
type agentMeters struct {
	mu     sync.Mutex
	meters map[string]*speedMeter
}

// record counts one streamed chunk of n bytes from sub-agent id.
func (a *agentMeters) record(id string, n int, now time.Time) {
	if a == nil || id == "" || n <= 0 {
		return
	}
	a.mu.Lock()
	s := a.meters[id]
	if s == nil {
		if a.meters == nil {
			a.meters = map[string]*speedMeter{}
		}
		s = &speedMeter{}
		a.meters[id] = s
	}
	a.mu.Unlock()
	s.record(n, now)
}

// finish drops sub-agent id's meter and returns what it measured: the tokens
// it streamed and its average generation rate. ok is false when it streamed
// nothing.
func (a *agentMeters) finish(id string, now time.Time) (tokens, rate float64, ok bool) {
	if a == nil {
		return 0, 0, false
	}
	a.mu.Lock()
	s := a.meters[id]
	delete(a.meters, id)
	a.mu.Unlock()
	if s == nil {
		return 0, 0, false
	}
	r, ok := s.read(now)
	return r.Total, r.Avg, ok
}

// withStreamStats adds what the sub-agent's meter measured to a completion
// event: the rate, and the output count when the backend reported no usage.
// The meter is dropped on failure too, so it does not outlive the agent.
func (m Model) withStreamStats(ev chat.AgentEvent) chat.AgentEvent {
	if ev.Status != chat.AgentStatusCompleted && ev.Status != chat.AgentStatusFailed {
		return ev
	}
	tokens, rate, ok := m.agentSpeed.finish(ev.ID, time.Now())
	if !ok {
		return ev
	}
	ev.TokensPerSec = rate
	if ev.OutputTokens <= 0 {
		ev.OutputTokens = int(math.Round(tokens))
		ev.OutputEstimated = true
	}
	return ev
}
