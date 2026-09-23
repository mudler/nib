package theme

import (
	"strconv"
	"time"
)

// ThoughtSummary is the text of a folded thought line: "thought for 4s", or
// "thought" when the time is not known (the trace arrived in one piece, with
// no streamed start to time it from).
func ThoughtSummary(d time.Duration) string {
	if d <= 0 {
		return ThoughtLabel
	}
	return ThoughtFor + Elapsed(d)
}

// Elapsed renders a duration to the second: "4s", "2m 5s". It rounds to the
// nearest second and never reads below "1s".
func Elapsed(d time.Duration) string {
	secs := int((d + time.Second/2) / time.Second)
	if secs < 1 {
		secs = 1
	}
	if secs < 60 {
		return strconv.Itoa(secs) + "s"
	}
	return strconv.Itoa(secs/60) + "m " + strconv.Itoa(secs%60) + "s"
}
