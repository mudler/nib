# Activity footer UX design

**Date:** 1 October 2026
**Status:** Draft — design direction approved

## Purpose

The footer tells a developer what nib is doing now. It also provides a quiet
path to detailed activity records.

The compact footer uses ordinary product language. It does not expose callback,
receipt, stream, or transport terms.

## Design principles

- Show current state before historical activity.
- Describe a duration only when nib knows when the current phase started.
- Put completed work and technical timing behind progressive disclosure.
- Keep the calm, warm-editorial style and the existing no-emoji rule.
- Preserve meaning without color.
- Keep keyboard behavior predictable on narrow and restricted terminals.

## Footer structure

The footer has two activity levels.

### Current-state row

The first row is pinned and noninteractive. It contains one authoritative state
and, when known, the elapsed time in that state.

```text
◐ Working · 12m
```

The state marker communicates whether work can make progress. **Working** and
**Running** use animated progress frames through the existing glyph-profile
mechanism. The example shows one frame. Every other state uses a stable marker.
**Ready** and blocked states never animate.

The theme defines stable markers for **Ready**, **Waiting**, **Approval**,
**Parked**, and **Interrupting**. Each marker has a non-color meaning and an
ASCII form in the existing restricted glyph profile. The implementation must
not use animation as the only distinction between two states.

The row must not show a historical point event. For example, it must never show
`Working · tool started 12m ago`.

### Activity row

The second row contains live work and one subdued history entry.

```text
todo 1/4   History 20                                      ctrl+g details
```

Live chips remain visible while their work runs. Completed shell jobs and
sub-agents move into **History**. The history count includes successful and
failed terminal jobs.

`Ctrl+G` focuses this row. `Enter` opens the selected item. The existing arrow,
`Tab`, `Esc`, and typing behavior does not change.

## State model

The model derives the current state from authoritative UI lifecycle fields. It
does not infer state from text, silence, or the latest received event.

State priority remains:

1. Interrupting
2. Approval
3. Waiting for an answer
4. Running one or more root tools
5. Working
6. Parked
7. Ready

The model stores `phaseStartedAt` separately from event receipt timestamps. It
sets this value when the displayed lifecycle condition begins. It resets the
value when that condition changes.

The lifecycle condition includes the state and displayed tool identity. For a
multi-tool state, it also includes the displayed tool count. A change from
`Running bash` to `Running read` resets the value. A change from `2 tools active`
to `3 tools active` also resets it. Duration ticks and event receipts do not
reset it.

A change from `Running bash` to `Working` starts a new **Working** phase. A
change from `Working` to `Running bash` starts a new **Running** phase. A tool
receipt timestamp cannot supply either phase start.

Restored sessions and incomplete lifecycle data can have an unknown phase start.
The compact row omits the duration in that case. It must not display `0s`,
`unknown`, or an age from another source.

The elapsed value uses a monotonic clock during a process lifetime. It clamps a
negative restored value to zero. The one-second footer tick refreshes the value.

## Copy matrix

| Lifecycle condition | Full state | Compact state | Duration |
|---|---|---|---|
| No turn or prompt is active | `Ready` | `Ready` | Omit |
| The model is processing | `Working` | `Working` | Current Working phase |
| One named root tool is active | `Running <tool>` | `Running` | Current tool phase |
| Multiple root tools are active | `<N> tools active` | `Tools active` | Current multi-tool phase |
| An ask prompt needs input | `Waiting for your answer` | `Answer needed` | Current waiting phase |
| A tool needs approval | `Approval needed` | `Approval` | Current approval phase |
| The foreground run is parked | `Parked` | `Parked` | Current parked phase |
| An interrupt is in progress | `Interrupting` | `Interrupting` | Current interrupt phase |

Durations use the existing compact units, such as `8s`, `12m`, and `1h 04m`.
The row joins the state and duration with ` · `.

## State markers

| State | Marker behavior | Required meaning without color |
|---|---|---|
| `Working` | Animate with configured progress frames | Active model progress |
| `Running <tool>` or `<N> tools active` | Animate with configured progress frames | Active tool progress |
| `Ready` | Stable ready marker | No active turn or prompt |
| `Waiting for your answer` | Stable question marker | User input is required |
| `Approval needed` | Stable approval marker | User approval is required |
| `Parked` | Stable pause marker | Foreground work is parked |
| `Interrupting` | Stable stop marker | An interrupt is in progress |

The full glyph profile can use typographic marks. The restricted profile uses
ASCII marks with the same meanings. The state text remains present, so a marker
never carries meaning alone.

## Activity copy

| Condition | Chip copy | Style and action |
|---|---|---|
| Todo list is empty | `todo –` | Subdued; opens the todo panel |
| Todo work exists | `todo <done>/<total> <item>` | Active when an item is active |
| Shell jobs run | `shell <N> running` | Active; opens the relevant shell logs |
| A sub-agent runs | `<type>: <title> · <step> · <tokens>` | Active; opens that agent log |
| Completed shell or agent jobs exist | `History <N>` | Subdued; opens activity details |
| No completed jobs exist | No history chip | No empty `History 0` chip |
| Failures are unseen | `History <N> ×<failed>` | Error count remains visible and has priority |
| Loops or a goal exist | Existing loop or goal copy | Existing action and state behavior |

The history count is the number of retained shell and sub-agent jobs that have
finished. Root tool calls stay in the transcript and do not increase this
count. This definition avoids a number that changes when transcript entries are
pruned.

The history entry replaces `shell <N> done` and `agents <N> done`. Running work
does not increase its count until that work reaches a terminal state.

## Activity details

Selecting **History** opens one activity view. The view groups shell jobs and
sub-agents. It shows exact logs, completion state, failures, and available event
timing.

The detail view can use technical labels because the user requested details.
It must distinguish these times:

- the time when nib received an event;
- the time when the current phase started;
- the time when model text last arrived.

The view must label missing timing as unavailable. It must not convert missing
data to zero.

Opening the history view marks visible failures as seen. The failure record
remains in the view after its alert clears.

Direct live chips keep their current behavior. A running agent chip opens that
agent's log. A running shell chip opens the shell log list at the best matching
job.

## Responsive behavior

The current-state row remains one terminal row. The renderer removes content in
this order:

1. Remove the duration.
2. Replace the full state with its compact state.
3. Truncate the compact state to the available cell width.

The renderer never replaces current state with history or technical timing.

The activity row keeps the existing fitting order. It first shortens long live
labels. It then removes idle items that have no alert and no selection. It
shortens remaining labels only after those steps.

The renderer never cuts a failure count. A selected item also remains visible.
If the width cannot fit the details hint beside a useful chip, the renderer
removes the hint.

The renderer measures terminal cells, not bytes or runes. It strips control
sequences from labels before it calculates width.

## Errors and accessibility

- A failure uses text and a count, such as `×2`. Color is supplemental.
- Active and selected items use a glyph or cursor in addition to color.
- The restricted glyph profile supplies ASCII markers and spinner frames.
- Only **Working** and **Running** animate. Restricted terminals use the
  existing discrete ASCII animation style.
- User-facing render helpers do not add emoji.
- Every row stays usable with terminal colors disabled.
- Control characters and callback text cannot enter compact labels.
- Focus moves first to an unseen failure, then to live work, then to the first
  available chip.
- Keyboard access does not depend on pointer input.

The implementation must preserve terminal-theme compatibility. It must not add
a background color to the footer.

## Migration

Implementation requires these changes:

1. Add explicit phase-start data to the TUI lifecycle state.
2. Remove root receipt age from the compact summary projection.
3. Add the state marker and phase duration to the summary presentation data.
4. Replace completed shell and agent chips with one history projection.
5. Add the unified history route to the existing activity details flow.
6. Keep receipt and event timing in the detail view only.
7. Update `README.md` with the new examples and interaction text.
8. Run `make sync-readme` to update `selfdoc/README.md`.

The implementation must commit `README.md` and `selfdoc/README.md` together.
This design task does not change either file.

## Test plan

Add or update tests for these cases:

- Each lifecycle condition maps to the copy in the state matrix.
- State priority remains deterministic when conditions overlap.
- A phase transition resets the phase duration.
- A running tool identity or tool count change resets the phase duration.
- Duration ticks and event receipts do not reset the phase duration.
- A tool completion starts a new **Working** phase when loading continues.
- An event receipt does not change the phase start.
- An unknown phase start omits the duration.
- A restored or future timestamp never produces a negative duration.
- The current-state row refreshes during silent work.
- The current-state row never contains callback, receipt, stream, or event copy.
- Completed shell jobs and sub-agents produce one **History** count.
- Running jobs stay in live chips and do not enter history early.
- Failed jobs contribute to history and retain the unseen failure count.
- `Ctrl+G` prioritizes a failure and `Enter` opens activity details.
- Activity details contain exact logs and clearly labeled timing sources.
- Width tests cover the full state, no duration, compact state, and truncation.
- Activity fitting keeps selected chips and failure counts.
- ANSI, control characters, wide glyphs, and widths from 1 to 99 stay on one row.
- The ASCII profile preserves state and failure meaning.
- **Working** and **Running** animate with full and ASCII progress frames.
- **Ready**, **Waiting**, **Approval**, **Parked**, and **Interrupting** use
  stable markers in full and ASCII profiles.
- Repeated renders of a stable state keep the same marker.
- Full and inline presenters produce equivalent footer information.
- Footer height matches the rendered rows at constrained frame sizes.
- README embedding and built-in documentation tests pass after documentation
  synchronization.

Run `go test ./...` after implementation.

## Non-goals

- Do not diagnose a stalled model from elapsed time alone.
- Do not expose provider callback names in the compact footer.
- Do not change transcript rendering or tool block lifecycle.
- Do not change job retention policy.
- Do not change `Ctrl+G`, `Ctrl+O`, or agent-message behavior.
- Do not add mouse-only controls, emoji, or a new color system.
- Do not add a motion preference that nib does not already support.
