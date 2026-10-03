# Session-owned goal supervision and terminal-state delivery

## Summary

nib will supervise a parked `/goal` turn while its delegated work is still in
progress. The supervisor belongs to `chat.Session`; it does not require a Cogito
dependency change. Consequently, the same behavior applies to every host that
uses a session: TUI, interactive and piped CLI, JSON mode, MCP, and embedded
use.

The implementation also introduces a session-level terminal-state barrier for
**every** turn that uses background agents or shell jobs. A successful
`SendMessage` return means nib has incorporated every relevant completion into a
model request and has reached one atomic, stable snapshot. This closes the race
where work finishes during what otherwise appears to be the root model's final
request.

This is one design with two related mechanisms:

- the goal supervisor decides when a parked goal needs a proactive model review;
- the terminal-state barrier decides when any turn may successfully return.

Neither mechanism runs a second root turn concurrently with `SendMessage`.

## User-visible behavior

When an active goal parks with background work still running, nib schedules
check-ins using `goal.check_in_delays`. The default is:

```yaml
goal:
  check_in_delays: [2m, 5m, 10m]
```

The first quiet period uses two minutes, the second five, and subsequent quiet
periods ten. The final value repeats indefinitely; there is no forced timeout.
A one-value list is therefore a fixed interval.

A background agent or shell job starting, succeeding, or failing requests an
immediate review and resets the next quiet-period delay to the first configured
value. If the root is currently making a model request, the review waits behind
that request rather than starting another one. Simultaneous events and a timer
firing collapse into one queued review.

Every supervisor-generated review produces a short visible assistant update
whose first bytes are `Goal check-in:`. A completion of the last background item
still wakes the parked root immediately through its durable completion notice;
the existing `/goal` stop gate then makes the root verify or continue. It does
not start a timer-only supervisor pass after there is no background work. This
resolves the apparent tension between immediate completion review and the rule
that the supervisor only runs while work remains.

The supervisor ends when background work is gone, the goal is completed,
cleared, replaced, or paused, the turn is interrupted, or the session closes.
Replacing a goal invalidates the old goal turn; supervision begins for the new
goal only when its own root turn parks with background work.

`/goal resume` resumes a paused goal. `Ctrl+C` interrupts the current turn and
pauses, rather than clears, its goal. Existing user documentation that says
otherwise must be corrected during implementation.

## Scope and ownership

### `chat.Session`

`Session` owns all supervision and terminal-barrier state because it already
owns the root turn, goal lifecycle, injection path, agent manager, and shell-job
registry. The implementation adds no scheduler or lifecycle behavior to Cogito.
Cogito remains responsible for executing one root run and its agents; Session
wraps the relevant callbacks and controls when that run may return.

Session provides these conceptual operations (names may follow local Go naming,
but the boundaries are normative):

```go
type backgroundEventKind int // start, success, failure

type sequencedNotice struct {
    sequence uint64
    source   noticeSource // agent or shell
    content  string
}

// Called by agent and shell lifecycle adapters.
func (s *Session) backgroundEvent(kind backgroundEventKind, notice *sequencedNotice)

// Called at each root request boundary after notices/check-ins have been copied
// into the request that is about to be sent.
func (s *Session) markRootObserved(sequence uint64)

// Waits/resumes until a stable terminal snapshot, or returns ctx.Err().
func (s *Session) awaitTerminalState(ctx context.Context) error

// Validates and atomically applies a replacement schedule.
func (s *Session) SetGoalCheckInDelays([]time.Duration) error
```

These operations share one session-owned mutex and wake mechanism. Callers must
not inspect several atomics or registries independently and infer terminal
state.

### Background lifecycle adapters

The agent callbacks and `ShellJobs` callback translate lifecycle changes into
`backgroundEvent`. Agent and shell paths use the same sequenced-notice queue and
the same state transition rules. A start has no completion notice, but it does
advance state and request a goal review. A completion or failure atomically
publishes its durable notice, updates running counts, advances state, and
requests a review where applicable.

The adapters must report transitions, not periodic snapshots. Duplicate source
callbacks are idempotent by source/job identity and terminal status, so they do
not create duplicate notices or sequence increments.

### Root-run driver

`SendMessage` remains the only driver of the root model conversation. Its
existing park/resume loop is extended to consume durable notices and supervisor
review requests before deciding to return. A supervisor wake-up resumes the
same root run and conversation fragment that `/goal` parked; it never invokes
`SendMessage` recursively and never launches a concurrent model request.

The driver records which event sequence the root request observed. It records
this only after all selected notices and the check-in instruction are present in
the immutable request handed to the model. Writing a notice to an injection
channel is not delivery.

### Configuration and settings

`types.Config` gains a goal configuration containing `check_in_delays`.
Configuration loading supplies `[2m, 5m, 10m]` when the key is absent or the configured list is empty. YAML uses Go duration strings and preserves list order.

The config validator rejects zero, negative, malformed, or overflowing durations. Its error identifies the exact entry, for example
`goal.check_in_delays[1]`, and includes the rejected value. There is no silent
fallback for a present invalid key.

`/settings goal.check_in_delays` displays the normalized comma-separated
schedule. Setting it accepts comma-separated duration values, trims surrounding
whitespace, and rejects empty elements. It uses the same parser and validator as
YAML loading. On success it:

1. persists the complete list to the writable YAML config;
2. applies the list to the live Session;
3. increments the supervisor generation and stops the old timer;
4. if an eligible goal turn is parked, restarts timing at the new first delay.

Validation occurs before persistence or live mutation, so failure changes
neither. If persistence succeeds but live application unexpectedly fails, the
command reports that restart is required and retains the persisted value; this
path is not expected after shared validation. Resetting the setting restores the
default schedule and follows the same live-update procedure.

## Unified synchronized state

The session maintains the following logical state under one mutex:

- `eventSequence`: monotonically increasing for every background start,
  completion, or failure and for atomic publication of its durable notice;
- `rootObservedSequence`: highest sequence incorporated into a root model
  request;
- running background agent identities and shell-job identities;
- durable, ordered completion notices and whether each is reserved for a request
  or consumed by a sent request;
- completion-notice publishers currently between callback entry and atomic queue
  publication;
- whether the root run is active, requesting, or parked;
- goal identity/generation and whether it is active, paused, or done;
- supervisor generation, schedule index, timer ownership, and whether a review
  is queued;
- close/interruption state.

An event sequence is session-local and need not persist after `Close`. Sequence
arithmetic must either use a practically non-wrapping representation or detect
overflow and fail safely; it must never wrap into a falsely stable equality.

Completion notices are durable for the life of the Session. The implementation
must not discard an undelivered notice because a channel is full or because a
presentation cap was reached. It may coalesce presentation text for a burst,
but each source completion remains represented, ordered, and covered by an
observed sequence. Existing bounded pending-notice behavior must therefore be
replaced or confined to display formatting rather than delivery correctness.

A notice has these states:

1. **publishing**: its completion callback has begun but has not atomically
   inserted the notice;
2. **queued**: durable and not yet selected for a request;
3. **reserved**: selected and copied into a root request being assembled;
4. **consumed**: the request containing it has been handed to the model.

A failed request returns its reserved notices to queued state in sequence order.
A successful or failed model response does not retroactively alter the fact that
its request observed those notices; normal model-error retry policy constructs a
new request from the same turn state as needed. The important delivery boundary
is incorporation into an actual model request, never an attempted channel
write.

## Goal-supervisor state machine

A goal supervisor instance is identified by the active goal identity and a
supervisor generation token. It has these states:

- **Inactive**: no eligible active goal turn.
- **Running**: the goal root is executing a model/tool step; no concurrent
  check-in can execute.
- **ParkedWaiting**: the same root turn is parked and background work remains;
  exactly one timer is armed unless a review is already queued.
- **ReviewQueued**: one timer/event review is pending; no second review can be
  queued.
- **Reviewing**: the root run has resumed and one model request contains the
  structured check-in prompt.
- **Stopped**: the generation has been invalidated and cannot publish.

Transitions are:

1. An active goal turn parks and work remains: enter `ParkedWaiting`, set the
   schedule index to zero if this is the first park/reset, and arm one timer.
2. A valid timer fires: under the mutex, verify session, goal, generation,
   parked state, and remaining work; atomically change to `ReviewQueued`; then
   perform a nonblocking wake. A stale timer does nothing.
3. A background start, success, or failure: advance event state; reset the
   schedule index to zero and invalidate the timer. If the goal turn is parked
   and work remains, queue one immediate review. If no work remains, rely on the
   completion notice and existing goal stop gate to resume verification, and
   stop supervision.
4. The root reaches its next safe request boundary: consume the single queued
   review, enter `Reviewing`, and add the structured prompt. After the pass, if
   the goal remains parked with work, enter `ParkedWaiting`, advance to the next
   delay, and arm it. Once the last configured delay is reached, keep using it.
5. Goal completion/clear/replacement/pause, interruption, no remaining work, or
   `Close`: enter `Stopped`, increment generation, stop and release the timer,
   clear a queued check-in, and wake any waiter that must re-evaluate state.

A background event during `Reviewing` records one queued follow-up and resets the
schedule, but cannot interrupt or overlap the current root request. Multiple
such events collapse into that one follow-up while their completion notices and
sequence increments remain distinct.

### Check-in request contract

The check-in is a harness-owned, structured user-role instruction distinguishable
from user input and completion notices. It tells the root to:

- begin its short visible output with exactly `Goal check-in:`;
- briefly report progress;
- inspect relevant agent and shell-job statuses rather than assume progress;
- identify failures, missing evidence, and newly unblocked work;
- follow up with existing work or spawn bounded additional work only when
  justified;
- continue waiting when that is the correct action; and
- call `goal_done` only after verifying the whole goal, not merely one subtask.

The instruction does not demand polling every worker, starting replacement work,
or declaring success. Tool calls may occur before the visible text, but each
completed supervisor pass must emit a short model output beginning with the
required prefix. If the model violates the prefix, nib prepends
`Goal check-in: ` to that pass's visible assistant text; it does not make a
second model call solely to repair formatting.

A model/backend error follows the existing root retry classification, backoff,
and limits. It does not create a parallel pass or consume a second supervisor
slot. A cancellation exits through the abnormal path described below.

## Terminal-state barrier

The barrier applies to every `SendMessage` turn that starts or inherits
background agents or background shell jobs, whether or not `/goal` is active.
It is evaluated while holding the unified state mutex. A successful return is
allowed only when one atomic snapshot satisfies all of these conditions:

1. no background agents are running;
2. no background shell jobs are running;
3. no completion-notice publisher is active;
4. no durable notice is queued, reserved but not incorporated, or otherwise
   unconsumed;
5. no supervisor check-in is queued or reviewing;
6. `rootObservedSequence == eventSequence`.

The running sets, not a separately sampled agent manager and shell registry, are
authoritative for this decision. Registration of a background start occurs
before its tool result can let the root proceed. Completion callback entry marks
the publisher active before terminal status can become externally visible;
queue insertion, running-set removal, sequence advancement, and publisher exit
are synchronized so there is no stable-looking gap.

Immediately before returning, `SendMessage` evaluates the predicate under the
mutex. If false, it keeps the same turn alive or parked. A completion during the
final root LLM request increments `eventSequence` and queues its notice. The
request's recorded observed sequence is therefore older; after the response,
the barrier forces another same-turn request containing the notice. A completion
that begins during the final check but has not published is covered by the
active-publisher condition.

A check-in may spawn new work. The start event updates the running set and
sequence before the request can pass the barrier, so the turn parks again and
cannot return early. Simultaneous agent and shell completions remain separate,
sequenced notices and are incorporated in order, even though they may cause only
one resumed model request and one queued goal review.

The barrier is not a promise of normal completion under cancellation.
Interruption, turn-context cancellation, process shutdown, and `Session.Close`
may return an error without terminal state. These paths invalidate supervision,
stop timers, cancel root/child work through existing lifecycle mechanisms, and
wake barrier waiters. They must not report a successful terminal reply.

## Host semantics

All hosts rely on the Session contract rather than reimplementing pending-work
checks:

- **TUI:** one `SendMessage` remains live while parked and supervised. Injected
  user follow-ups join that run through the existing serialized path. UI state
  may render waiting/check-in events, but must never launch a parallel root
  turn.
- **Interactive CLI:** a submitted turn remains attached to the live Session
  until the barrier succeeds or the user interrupts. Input may still be routed
  to the existing live run where supported.
- **Piped CLI:** EOF means there will be no further user messages; it is not
  permission to abandon background work. The process waits for the terminal
  barrier, then writes the final reply and exits normally.
- **JSON:** background, notice, check-in, and assistant events are emitted in
  their actual order. The final reply event is emitted only after the barrier;
  the usage record follows all events and the final reply.
- **MCP and embedding:** the call does not resolve successfully before the
  Session barrier. Cancellation remains the caller's bounded escape hatch; no
  host-specific timeout is introduced.

Hosts may format events differently, but they cannot weaken successful-return
semantics. The Session callback/event API must expose check-in output through the
same assistant streaming path as other root output.

## Timer, wake-up, and shutdown rules

Timers carry both goal identity and supervisor generation. Changing the
schedule, replacing/pausing/completing the goal, interrupting, or closing the
Session increments generation before stopping the timer. Timer callbacks check
the token under the mutex before changing state.

Timer publication is nonblocking. The callback first records `ReviewQueued`
under the mutex, then attempts a nonblocking wake on the existing run wake-up
mechanism. A full wake channel is harmless because queued state is durable and
the root checks it at every request/park boundary. This avoids leaked timer
goroutines and avoids correctness depending on channel capacity.

At most one timer object and one queued check-in exist per Session. Replacing a
timer stops the old one and drains or invalidates its callback through the
generation check. `Close` marks the state closed, invalidates all generations,
stops the timer, cancels the turn, unregisters or disables shell completion
callbacks as supported by their owner, wakes waiters, and waits for
session-owned supervisor callbacks to leave. No timer or supervisor goroutine
may survive `Close`.

## Failure handling

- Agent and shell failures are ordinary sequenced completion notices, request an
  immediate eligible goal review, and reset the schedule to its first delay.
- Model errors use existing retry behavior. Notices reserved for a request are
  not lost across retry or rollback.
- Duplicate lifecycle callbacks do not duplicate notices or reviews.
- Wake-channel saturation cannot lose a notice or check-in because channels are
  hints and synchronized state is authoritative.
- An invalid schedule is rejected with its indexed configuration key and does
  not partially apply.
- There is no supervisor deadline, maximum number of passes, automatic goal
  failure, or hidden timeout. The final configured interval repeats until a
  defined stop condition occurs.

## Testing strategy

Tests use fake clocks/timers and scripted model clients; unit tests do not sleep
for real schedule durations. Lifecycle tests use controllable agent and shell
fakes with callback barriers so each race can be placed precisely.

### Configuration and scheduler

Cover:

- absent configuration produces `[2m, 5m, 10m]`;
- one duration repeats as a fixed interval and a multi-value schedule advances,
  then repeats its final value;
- empty, malformed, zero, negative, and overflowing entries fail with the exact
  indexed key;
- `/settings` comma parsing, whitespace, persistence, reset, and atomic failure;
- live updates invalidate the old generation and restart at the first delay;
- start, success, and failure request immediate review and reset the schedule;
- timer/event simultaneity queues one review;
- bursts collapse reviews but retain all sequenced notices;
- stale timer callbacks cannot publish;
- there is at most one timer and one queued review;
- no work, paused/replaced/cleared/completed goals, interruption, and close stop
  supervision;
- the final interval repeats without a timeout; and
- check-in prompts contain the required review contract and visible output has
  exactly one `Goal check-in:` prefix.

### Terminal barrier and delivery races

For both agent and shell work, deterministically complete work:

- during the apparent final root model request;
- after response receipt but before the terminal snapshot;
- while a completion publisher is active but before queue insertion;
- while a notice is queued, reserved, returned after request failure, and
  finally consumed;
- simultaneously across multiple agents and shell jobs;
- during a supervisor check-in;
- after a check-in starts new background work; and
- after the root observed an older event sequence.

Each successful case asserts that `SendMessage` does not return early, every
notice is incorporated into a later model request in sequence, the root-observed
sequence catches the current sequence, and the final snapshot satisfies every
barrier predicate. Cancellation variants assert prompt abnormal return without
claiming terminal state. Close tests assert timers, callbacks, and waiters exit.

The narrow race tests should run repeatedly under the race detector, for
example:

```sh
go test -race -count=100 ./chat -run 'GoalSupervisor|TerminalBarrier'
```

The exact regular expression may track final test names, but CI must retain a
repeatable focused race target rather than relying only on one full-suite pass.

### Host coverage

Add host-level tests proving:

- piped CLI waits after EOF for agent and shell completion;
- interactive CLI remains interruptible while waiting;
- JSON emits all lifecycle/check-in/notice events and the final reply before
  usage;
- TUI does not dispatch a parallel turn during a check-in;
- MCP and embedded Session callers do not receive an early successful return.

Run targeted package tests, the focused repeated race tests, and finally:

```sh
go test ./...
```

During implementation, update README documentation for the schedule, host
behavior, `/goal resume`, and Ctrl+C pause semantics, then run
`make sync-readme` so `selfdoc/README.md` remains synchronized.

## Acceptance criteria

The change is accepted when all of the following are demonstrated by tests:

1. Goal supervision is implemented entirely at Session level with no Cogito
   dependency change and behaves identically through all supported hosts.
2. Only an active parked goal with background work is timer-supervised; ordinary
   no-work goal gating remains intact.
3. The validated configurable adaptive schedule, repeating final delay, and
   live `/settings` update semantics match this specification.
4. Lifecycle events reset timing and cause prompt review without permitting more
   than one timer, one queued review, or concurrent root model requests.
5. Reviews resume the same parked turn, use the required structured instruction,
   and emit the required short prefix.
6. Every normal successful `SendMessage` involving background work passes one
   atomic terminal-state snapshot containing all six barrier conditions.
7. Agent and shell completion notices are durable, sequenced, and considered
   delivered only when incorporated into a model request; final-request races
   cannot lose them.
8. Cancellation and shutdown return abnormally and leave no Session-owned timer
   or supervisor goroutine.
9. Piped CLI, interactive CLI, JSON, TUI, MCP, and embedding tests enforce their
   specified ordering and return behavior.
10. Focused race tests and `go test ./...` pass, and README/self-documentation
    remain synchronized.

## Non-goals

- Changing Cogito, its public API, or its dependency version.
- Running multiple root model requests concurrently or creating a second
  conversation for supervision.
- Supervising ordinary non-goal turns on a timer; they receive only the
  terminal-state barrier and durable completion delivery.
- Imposing a goal timeout, retry-count deadline, final-delay cutoff, or automatic
  declaration of success/failure.
- Treating check-ins as permission for unbounded delegation or unconditional
  replacement of failed work.
- Persisting active goals, timers, event sequences, or notices across process
  restarts.
- Changing tool approval, agent permissions, shell semantics, model selection,
  or usage accounting except for event ordering needed to place JSON usage last.
- Guaranteeing graceful terminal state after interruption, context cancellation,
  or shutdown.
