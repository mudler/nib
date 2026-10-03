# Reuse message rendering for reasoning traces

## Goal

nib must render reasoning traces with the same Markdown and streaming effects as
assistant messages.

This behavior applies to:

- The live reasoning trace.
- A completed thought that the user expands with `Ctrl+R`.

The reasoning header, box rule, collapse behavior, and thought summary remain
unchanged. The body adopts the assistant Markdown styles. The header and box
rule retain the reasoning color treatment.

## Current behavior

Assistant messages use a shared streaming pipeline. The pipeline reveals text at
a steady rate, renders partial Markdown, closes an unfinished code fence for the
preview, and adds a pulsing cursor.

Live reasoning uses the same reveal timing but wraps plain text. As a result,
Markdown syntax remains visible while the model reasons. Expanded completed
thoughts also wrap plain text.

## Design

The implementation must reuse the assistant message rendering code. It must not
add another Markdown parser or another partial-block renderer.

Markdown rendering remains in `package tui`. This package owns the cached
Glamour renderers and the streaming state. `package render` continues to own the
reasoning and thought chrome.

### Shared streaming renderer

Generalize the existing assistant streaming renderer so its caller supplies the
cursor. The shared implementation retains these behaviors:

- It renders settled blocks with the cached Markdown renderer.
- It renders the mutable final block as Markdown on each frame.
- It closes an unfinished fenced code block for the preview.
- It preserves Markdown spacing between settled and mutable blocks.
- It places the cursor after the last visible cell.

The assistant message path supplies a cursor based on `streamStart`. The live
reasoning path supplies a cursor based on `reasoningSince`.

Both paths continue to use the existing reveal functions. The change does not
add a second animation clock or a second easing algorithm.

### Live reasoning

The model renders the visible reasoning source as streaming Markdown before it
passes the view state to the presenter.

The model uses the width inside the reasoning box. It subtracts the four cells
used by the indent, box rule, and following space. The body width has a minimum
of one cell. Existing layout constraints keep normal content widths above four
cells. This change does not add new behavior for unsupported smaller widths.

The model stores the rendered ANSI body in a distinct view-state field. The
source text remains available for model state and does not become a presenter
contract.

The presenter applies the existing reasoning header and box rule to each
rendered line. It does not append another cursor, wrap the Markdown source, or
apply an outer text style to the rendered body.

Collapsed reasoning continues to show the trailing rendered lines. Therefore,
the cursor and newest output remain visible. The hidden-line count refers to
rendered terminal rows rather than Markdown source lines.

### Completed thoughts

A collapsed completed thought remains one summary line.

When the user expands a thought, the model renders its full source with the
existing final Markdown renderer. It passes the rendered ANSI body to the
thought presenter. The presenter neither wraps nor restyles that body. It adds
the existing thought summary and box rule. Completed thoughts do not show a
streaming cursor.

The live preview can temporarily close an unfinished code fence. The completed
thought always renders the authoritative source.

### Styling

The Markdown renderer keeps its existing inline styles, including headings,
emphasis, links, inline code, and highlighted code blocks. The reasoning chrome
keeps its existing style.

The presenter must not wrap the ANSI-rich Markdown body in one outer text style.
An outer style can override Markdown colors or break them after nested reset
sequences. The box rule and header retain the reasoning styles and distinguish
the trace from an assistant message.

## Data flow

1. The provider emits reasoning text.
2. The model appends the text to the existing reasoning buffer.
3. The animation clock advances the existing reasoning reveal position.
4. The shared streaming renderer converts the visible source to Markdown.
5. The presenter adds the reasoning header, box rule, and collapse hint.
6. When the step ends, the model stores the source in a completed thought.
7. An expanded thought uses the existing final Markdown renderer.

## Compatibility

The change preserves:

- Reasoning event ordering.
- The independent reveal positions for replies and reasoning.
- The default collapsed state.
- Trailing-line collapse behavior.
- `Ctrl+R` expansion and collapse.
- Thought placement before the related assistant message.
- Reasoning hit testing and scroll behavior.
- Inline and full presenter behavior.

## Tests

Add focused tests for these cases:

- Live headings, emphasis, lists, inline code, and fenced code render as
  Markdown instead of raw syntax.
- An unfinished live code fence renders as a code block.
- Live reasoning uses the shared reveal progression and shows exactly one
  cursor. Tests drive animation ticks directly and do not depend on wall-clock
  timing.
- Collapsed live reasoning shows the trailing rendered rows and the cursor.
- Expanded live reasoning shows all rendered rows.
- A collapsed completed thought shows only its summary.
- An expanded completed thought renders Markdown with no cursor.
- For complete Markdown source, live settled output matches completed output
  after the test removes the streaming cursor.
- Markdown ANSI styles survive the reasoning chrome. The box rule keeps its own
  style.
- Reasoning content stays within the available width for supported content
  widths.
- Existing assistant streaming output does not change.
- Both presenters satisfy their existing conformance tests.

Run `go test ./tui/...` first. Then run `go test ./...`.

## Documentation

No README change is required. This change makes reasoning output consistent with
existing message rendering and does not add a command or configuration option.

## Out of scope

- Changing the reasoning header or box chrome.
- Changing the default collapsed state.
- Adding per-thought expansion state.
- Changing event handling or reasoning storage.
- Changing the animation rate or easing algorithm.
- Moving Markdown rendering into `package render`.
- Changing Mermaid handling in partial streaming blocks.
