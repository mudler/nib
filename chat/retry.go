package chat

import (
	"context"
	"fmt"
	"math/rand/v2"
	"regexp"
	"strings"
	"time"

	"github.com/mudler/cogito"
)

// Backend failure resilience.
//
// A turn can run for many minutes and dozens of requests, so a backend hiccup
// must not end it. Errors are retried by default; only a small set of fatal
// errors stop immediately, because the same request fails the same way again.
//
// Errors fall into three classes (classifyBackendError):
//
//   - rate limit (HTTP 429): wait until the limit resets, then go on.
//   - transient (5xx, overloaded, connection reset, EOF, timeouts, unknown
//     errors): back off and go on. This is the default: an error that matches
//     no fatal marker is transient, so a new backend hiccup is retried even
//     before we have a name for it.
//   - fatal (context overflow, 400, 401, 403, unknown model, ...): stop,
//     because the same request fails the same way again.
//
// Two layers cooperate:
//
//  1. trackedLLM / trackedStreamingLLM (contextlive.go) retry one request a
//     few times with short waits, before cogito sees the error. Only short
//     waits belong here: cogito retries every failed call on its own, so each
//     second slept at this layer is multiplied by cogito's retry count, and
//     the user is told nothing while it happens.
//
//  2. SendMessage (session.go) owns the long waits. It announces each wait
//     through OnStatus, sleeps until the reset or the backoff ends (Ctrl+C
//     cancels), then runs the turn again FROM WHERE IT STOPPED: cogito returns
//     the fragment it had built when it failed, tool results included, so no
//     finished tool call runs twice. The turn gives up only after
//     turnRetryBudget failed attempts in a row that made no progress.
//
// A retry that still fails reaches humanizeTurnError (errors.go), which turns
// a 429 into an actionable message.

// backendErrorClass is the retry class of a backend error.
type backendErrorClass int

const (
	errFatal backendErrorClass = iota
	errRateLimited
	errTransient
)

const (
	// requestAttempts caps the calls the trackedLLM layer makes for a single
	// request before handing the error to cogito.
	requestAttempts = 3

	// requestMaxWait is the longest the trackedLLM layer sleeps before a
	// retry. A rate limit that resets later than this is left to the session
	// layer, which can tell the user.
	requestMaxWait = 30 * time.Second

	// turnRetryBudget is how many failed attempts in a row, with no progress
	// between them, one turn survives. An attempt that added messages to the
	// conversation resets the count.
	turnRetryBudget = 10

	// turnMaxWait is the longest the session layer waits before one retry.
	turnMaxWait = 15 * time.Minute

	// turnBaseWait is the first session-layer backoff when the error does not
	// say when to come back. It doubles on each retry up to turnBackoffCap.
	// The lower layers already spent their short retries, so starting shorter
	// would only repeat their failure.
	turnBaseWait   = 5 * time.Second
	turnBackoffCap = 5 * time.Minute
)

// retrySleep sleeps for d or until ctx is done. A variable so tests do not
// wait in real time.
var retrySleep = sleepCtx

// sleepCtx sleeps for d but returns early with ctx.Err() if ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// rateLimitMarkers are lower-case substrings that identify a 429 across the
// providers nib talks to:
//
//	LocalAI:   localai stream: status 429: {"error":{"message":"Rate limit exceeded ..."}}
//	OpenAI:    error, status code: 429, status: 429 Too Many Requests, message: ...
//	Anthropic: {"type":"error","error":{"type":"rate_limit_error",...}}
var rateLimitMarkers = []string{
	"too many requests",
	"rate limit",
	"rate_limit",
}

// statusRe finds the HTTP status in "status 429:" (LocalAI) and
// "status code: 429" (OpenAI SDK) error strings.
var statusRe = regexp.MustCompile(`status(?: code:)? (\d{3})\b`)

// fatalMarkers are lower-case substrings of errors where retrying cannot help:
// the request itself is wrong, so the same call fails the same way again.
// Everything that matches no marker is retried — see classifyBackendError.
var fatalMarkers = []string{
	// cogito's text for a tool call to a tool the model hallucinated;
	// not a backend error, and the model will not self-correct on retry.
	"not found",
}

// classifyBackendError sorts err into a retry class. Errors are matched as
// strings because cogito stringifies the provider error before it reaches
// the session loop. A context overflow is fatal here even when the backend
// reports it as a 500: it has its own recovery, and repeating the request
// cannot fix it.
//
// The default is transient: an error that matches no known fatal marker is
// retried, so a new backend hiccup is covered even before we have a name for
// it. Only the small set above — client-side failures that never succeed on
// retry — stops immediately.
func classifyBackendError(err error) backendErrorClass {
	if err == nil || isContextOverflow(err) {
		return errFatal
	}
	low := strings.ToLower(err.Error())
	if m := statusRe.FindStringSubmatch(low); m != nil {
		switch code := m[1]; {
		case code == "429":
			return errRateLimited
		case code == "408" || code[0] == '5':
			return errTransient
		default:
			// 4xx (except 429/408): client error, same request fails again.
			return errFatal
		}
	}
	for _, m := range rateLimitMarkers {
		if strings.Contains(low, m) {
			return errRateLimited
		}
	}
	for _, m := range fatalMarkers {
		if strings.Contains(low, m) {
			return errFatal
		}
	}
	// Default: an unrecognized error is transient. A new backend hiccup
	// is retried even before we have a name for it.
	return errTransient
}

// isRateLimitError reports whether err is a rate-limit (HTTP 429) failure.
func isRateLimitError(err error) bool {
	return classifyBackendError(err) == errRateLimited
}

// rateLimitResetRe extracts the "Limit resets at: 2026-09-21 15:48:40 UTC"
// timestamp from a LocalAI rate-limit error body. The timestamp is in the
// HTTP response body, not a header, so it is parsed from the error string.
var rateLimitResetRe = regexp.MustCompile(
	`(?i)limit resets at:\s*(\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2})\s+UTC`)

// rateLimitResetWait parses the "Limit resets at:" timestamp from the error
// and returns the duration until that moment. Returns false if the timestamp
// is absent or in the past.
func rateLimitResetWait(err error) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}
	m := rateLimitResetRe.FindStringSubmatch(err.Error())
	if m == nil {
		return 0, false
	}
	t, perr := time.ParseInLocation("2006-01-02 15:04:05", m[1], time.UTC)
	if perr != nil {
		return 0, false
	}
	wait := time.Until(t)
	if wait <= 0 {
		return 0, false
	}
	return wait, true
}

// requestWait returns how long the trackedLLM layer waits before retry number
// attempt (0-based), and false when the error is fatal or the rate limit
// resets too late to wait for at this layer. Otherwise it backs off 2s, 4s.
func requestWait(err error, attempt int) (time.Duration, bool) {
	if classifyBackendError(err) == errFatal {
		return 0, false
	}
	if wait, ok := rateLimitResetWait(err); ok {
		if wait > requestMaxWait {
			return 0, false
		}
		// The reset timestamp has one-second resolution; do not arrive early.
		return wait + time.Second, true
	}
	d := time.Duration(1<<(attempt+1)) * time.Second
	if d > requestMaxWait {
		d = requestMaxWait
	}
	return d, true
}

// turnWait returns how long the session layer waits before retry number retry
// (0-based) of a turn that failed with err.
func turnWait(err error, retry int) time.Duration {
	if wait, ok := rateLimitResetWait(err); ok {
		wait += time.Second
		if wait > turnMaxWait {
			return turnMaxWait
		}
		return wait
	}
	if retry > 16 {
		retry = 16
	}
	d := turnBaseWait << retry
	if d > turnBackoffCap {
		d = turnBackoffCap
	}
	// Up to 20% jitter, so sessions that failed together do not all come
	// back in the same second.
	return d + time.Duration(rand.Int64N(int64(d)/5+1))
}

// turnRetryStatus is the line shown while the session layer waits.
func turnRetryStatus(err error, wait time.Duration, retry int) string {
	what := "Backend unavailable"
	if isRateLimitError(err) {
		what = "Rate limited"
	}
	return fmt.Sprintf("%s — retrying in %s (%d/%d, ctrl+c to stop)…",
		what, wait.Round(time.Second), retry+1, turnRetryBudget)
}

// retryResumeStatus replaces the countdown once the wait ends. Nothing else
// is sure to replace it: a retry that streams a plain answer emits no status
// of its own, so the countdown would stay on screen for the rest of the turn.
const retryResumeStatus = "Thinking…"

// waitTurnRetry sleeps for wait before a turn retry, re-announcing the status
// each second so the countdown the user sees goes down. When the wait ends it
// announces retryResumeStatus. The wait is measured in retrySleep steps, not
// wall time, so a stubbed sleep in tests ends it at once.
func waitTurnRetry(ctx context.Context, err error, wait time.Duration, retry int, announce func(string)) error {
	for left := wait; left > 0; {
		announce(turnRetryStatus(err, left, retry))
		step := left % time.Second
		if step == 0 {
			step = time.Second
		}
		if serr := retrySleep(ctx, step); serr != nil {
			return serr
		}
		left -= step
	}
	announce(retryResumeStatus)
	return nil
}

// rateLimitMessage builds a user-facing message for a terminal 429 error. If
// the error carries a reset time, the message includes it.
func rateLimitMessage(err error) string {
	if wait, ok := rateLimitResetWait(err); ok {
		return fmt.Sprintf(
			"the backend rate-limited the request (limit resets in %s). "+
				"Wait and retry, or switch to a different endpoint",
			wait.Round(time.Second))
	}
	return "the backend rate-limited the request. Wait and retry, or switch to a different endpoint"
}

// retryRequest calls fn up to requestAttempts times, retrying rate-limited
// and transient errors whose wait is short enough for this layer. Fatal
// errors are returned at once. Cancelling ctx stops the backoff.
func retryRequest[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	var zero T
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		result, err := fn()
		if err == nil || attempt+1 >= requestAttempts {
			return result, err
		}
		wait, ok := requestWait(err, attempt)
		if !ok {
			return result, err
		}
		if serr := retrySleep(ctx, wait); serr != nil {
			return zero, serr
		}
	}
}

// canRetryTurn reports whether the session should run the turn again after
// err. It requires the turn context to still be alive: a cancelled turnCtx
// means the user pressed Ctrl+C, and re-sending is the opposite of what they
// asked for.
func canRetryTurn(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	return classifyBackendError(err) != errFatal
}

// resumableFragment picks the fragment a retried turn continues from. cogito
// returns the fragment it had built when it failed; when that fragment holds
// everything the attempt started from plus finished work, the retry continues
// from it so no tool call runs twice. Otherwise the retry starts over from
// started. The bool reports whether the attempt made progress.
func resumableFragment(started, failed cogito.Fragment) (cogito.Fragment, bool) {
	if len(failed.Messages) <= len(started.Messages) {
		return started, false
	}
	// An assistant message that requested tools must be followed by their
	// results; a fragment that stops right after it cannot be sent again.
	last := failed.Messages[len(failed.Messages)-1]
	if last.Role == "assistant" && len(last.ToolCalls) > 0 {
		return started, false
	}
	return failed, true
}
