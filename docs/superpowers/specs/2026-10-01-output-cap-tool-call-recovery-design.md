# Recover tool calls that reach the output cap

## Goal

A large tool call must not fail immediately when it reaches the model's output
cap. nib must ask the model to split the call into smaller operations before it
reports an error.

The recovery must preserve the safe output reservation. It must compact the
conversation only when compaction can create more output room.

## Design

`Session.SendMessage` already classifies a truncated tool call as one of these
causes:

- The context window ran out while the model wrote the call.
- Reasoning filled the context window before the call completed.
- The call reached the request's output cap while the context window still had
  room.

The first output-cap truncation must no longer end the turn. nib must retry once
without raising the safe `max_tokens` value. The context clamp can lower the
reservation to account for the transient note. The retry must include a
user-role note that tells the model to split the operation into smaller tool
calls. For file changes, the note can suggest several `write` or `edit` calls.
The note must not enter the stored conversation history.

If the retry reaches the output cap again, nib must inspect the request state:

1. If nib clamped the output reservation to fit the context window, and the
   prompt is large enough to benefit, nib must run the existing compaction
   recovery chain. It then retries with the split-call note.
2. If the request used the model's output cap, nib must not compact. Compaction
   cannot increase that cap.
3. If compaction cannot change the next request, nib must stop instead of
   sending the same request again.

The recovery must remain bounded. One split retry and one useful compaction
retry are the maximum additional attempts for this truncation path.

## State and data flow

The turn must track these facts separately:

- Whether it already sent the split-call retry.
- Whether it already used compaction for repeated output-cap truncation.
- Whether the request's output reservation was clamped by the context window.

The existing `live.lastClamped()` value identifies a context-limited output
reservation. The existing truncation report supplies prompt and completion
usage. The existing `compactableShare` rule decides whether the prompt is large
enough for compaction to help.

The retry must preserve completed tool calls from the current run through the
existing resumable-fragment path. It must not execute completed calls again.

## Error handling

If recovery is exhausted, nib must return an actionable error. The message must
state that the tool call still exceeded the output limit after a split retry.

The message must not tell the user to raise `max_tokens` when nib knows the
request used the model's output cap. It should tell the user to request smaller
changes or use a model with a larger output limit.

An interrupted turn must not retry or compact.

## Tests

Tests must cover these cases:

- The first output-cap truncation retries with a split-call note.
- A model that follows the note can finish the turn.
- The retry keeps the same safe output reservation.
- A repeated context-clamped truncation compacts and retries once.
- A repeated truncation at the model's output cap does not compact.
- Recovery stops after the bounded retries.
- The final error describes the attempted split recovery.
- The transient note does not enter persistent history.
- Completed tool calls are not executed again during recovery.

Run the focused chat tests first. Then run `go test ./...`.

## Documentation

Update the compaction and truncation section in `README.md`. State that nib
first asks the model to split a call that reaches the output cap. State that nib
compacts after a repeated truncation only when the context window reduced the
output reservation.

Run `make sync-readme` after the README update.

## Out of scope

- Raising the model's discovered output cap.
- Splitting JSON tool arguments inside nib.
- Executing a partially generated tool call.
- Compacting when the model's output cap is the limiting value.
