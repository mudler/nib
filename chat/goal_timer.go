package chat

import "time"

// goalTimer is the deliberately small timer surface used by goal supervision.
// Implementations must permit Stop to race with the callback in the same way as
// time.Timer.
type goalTimer interface {
	Stop() bool
}

// goalTimerFactory makes goal timers. Keeping construction behind this seam
// lets supervisor tests advance time without sleeping.
type goalTimerFactory interface {
	AfterFunc(time.Duration, func()) goalTimer
}

type realGoalTimerFactory struct{}

func (realGoalTimerFactory) AfterFunc(delay time.Duration, fn func()) goalTimer {
	return time.AfterFunc(delay, fn)
}
