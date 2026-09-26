# Footer: activity strip, focus mode, configurable telemetry

## Problem

The footer packs key hints and every telemetry badge into one line, then adds a
separately formatted row per background area (sub-agents, shell jobs, loops,
goal, todos). It is hard to tell what is running now and what is history, and
nothing in the footer says how to look closer: the todo panel (ctrl+t) is not
mentioned anywhere.

## Layout

```
ctx ▰▰▰▰▰▰▰▰│▰▱ 88k/100k · compacts at 76.7k  tok/s 7.6 · avg 13      front telemetry
session 96.8k in / 27.7k out  age 5h 12m  16:29:49  cpu 27%  mem …   expanded (focus only)
◐ todo 0/8 Review existing repo st…  ▷ shell ✗1  ◇ agents –  ⟳ loops 2  ◎ goal …
enter send · ctrl+y use command · G/end newest · ctrl+c twice exit     ctrl+g activity
```

1. **Front telemetry.** The badges named in `ui.footer_front` (default
   `context,speed`), on their own line, with the same width fallbacks as
   today.
2. **Expanded telemetry.** The badges named in `ui.footer_expanded` (default
   `usage,age,clock,cpu,mem`). Shown only while the activity strip has focus.
3. **Activity strip.** One chip per area. todo and shell always show (dim `–`
   when empty), so the line does not jump. Each running sub-agent has its own
   chip: type, a title from the first sentence of its task, its current step
   (the tool it called, or thinking / writing), and its output so far, e.g.
   `↳ explore: scan the LoRA loader · bash · 3.1k`. A sub-agent history chip
   (`agents N done`) stands in while none runs, and stays beside running ones
   while a failure is unseen, so an alert never sits on an agent that did not
   fail. loops and goal show only when they exist. Chip states:
   - running: bright, with the running count,
   - failed: red `✗N` for failures not yet looked at (opening the chip's
     detail view marks them seen),
   - idle: dim.
   When the strip is too wide, labels shorten (widest first) down to 10
   cells, then idle chips without an alert drop, then labels shorten further.
   The selected chip shows the cursor in its glyph's place, so moving the
   focus never shifts the strip.
4. **Help line.** Unchanged hints on the left, `ctrl+g activity` on the right.
   When the help line has no room for the hint, it goes at the end of the
   strip instead, so the strip stays reachable on a narrow terminal.

Telemetry items: `context`, `speed`, `usage`, `age` (session age, e.g.
`7d 5h`), `clock`, `cpu`, `mem`. An item can be in either list. Unknown items
are ignored. `ui.hide_hud` still hides clock, cpu and mem wherever they are.

## Focus mode

- `ctrl+g` focuses the strip (and shows the expanded telemetry line). The first
  chip that needs attention is selected: failed, then running, then the first.
- `←`/`→`, `tab`/`shift+tab` move between chips.
- `enter` opens the selected chip's detail view:
  - todo: the todo panel,
  - shell / agents: the log viewer with the most relevant job of that kind
    selected (running, then failed, then the newest),
  - loops: a panel listing each loop (id, schedule, prompt, self-paced count),
  - goal: a panel with the full goal, its state, and the /goal commands.
- `esc` or `ctrl+g` leaves focus. Any other key leaves focus and goes to the
  composer, so typing is never lost.

## Follow-ups

- Talk to a running sub-agent: cogito's `AgentManager.Inject(id, message)`
  already feeds a user message into its loop. The agent's log view could take
  input and send it there.
- Model-generated agent titles, e.g. from the classifier model when one is
  configured, instead of the task's first sentence.
