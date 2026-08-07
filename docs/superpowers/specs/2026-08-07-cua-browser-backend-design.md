# Cua browser backend

**Date:** 2026-08-07
**Status:** Approved design; implementation plan in `docs/superpowers/plans/2026-08-07-cua-browser-backend.md`

## Problem

nib's opt-in browser tools currently launch and control a dedicated headed
Chromium process directly through Chromedp. The implementation is intentionally
small and model-friendly, but it owns browser discovery, profile lifecycle,
CDP state, accessibility snapshots, element references, action delivery, and
cross-platform behavior itself.

Cua Driver 0.19 introduced a typed browser-use surface that overlaps with this
implementation and adds exact native-process/window/tab binding, inactive-tab
operation, semantic snapshots, frame-aware references, structured refusals,
dialogs, file assignment, approved downloads, and richer pointer actions.
nib already uses Cua Driver for `computer_use`, but that adapter currently owns
its Cua process privately and cannot share it with a browser backend.

## Goal

Add an opt-in Cua-backed browser implementation while retaining Chromedp as the
default. Keep nib's compact existing browser tools, add selected Cua browser
capabilities through focused nib tools, and share one Cua runtime and declared
session between browser and native computer control.

## Success criteria

1. `browser.backend` defaults to Chromedp; `browser.backend: cua` explicitly
   selects Cua and never silently falls back.
2. The existing seven browser tools keep their names, input schemas, compact
   refs, automatic post-action snapshots, and URL safety policy.
3. The Cua backend additionally exposes logical tab management, pointer
   actions, JavaScript dialog handling, file-input assignment, and downloads.
4. Browser-only Cua and combined browser/computer Cua configurations both work;
   the combined case owns exactly one Cua connection and one declared session.
5. The Cua backend uses only a driver-managed persistent `isolated_named`
   profile. Existing personal profiles are out of scope.
6. Normal CI remains hermetic, the Chromedp backend remains covered, and an
   opt-in live Cua integration suite covers the real browser route.

## Non-goals

- Making Cua the default browser backend in this release.
- Removing Chromedp or its tests.
- Exposing Cua's raw browser MCP tools directly to the model.
- Attaching to, copying, migrating, or modifying an existing personal browser
  profile.
- Supporting arbitrary filesystem paths for uploads or downloads.
- Adding a live Cua browser job to normal GitHub Actions.
- Hiding a requested Cua backend failure by falling back to Chromedp.

## Chosen architecture

### Shared Cua runtime

Introduce an internal `cuaRuntime` that owns exactly one Cua Driver MCP client
session for the lifetime of a nib session. `StartTransports` creates it when
either of these conditions is true:

- `Config.Computer.Enabled`; or
- `Config.Browser.Enabled && Config.Browser.Backend == "cua"`.

The runtime is responsible for:

- resolving the Cua executable, arguments, environment, and declared session;
- starting and closing the `cua-driver mcp` child connection;
- when the Cua browser backend is enabled, validating Cua Driver version
  0.19.0 or newer and verifying its complete browser dependency set;
- starting and ending the declared Cua session;
- applying shared runtime configuration and health probing; and
- scrubbing provider API keys from the child environment.

Runtime construction is synchronous. If Cua is missing or fails to connect,
`StartTransports` returns an actionable error before it returns any computer or
browser transport. The 0.19.0 version floor and browser tool-set validation
apply only when the Cua browser backend is selected; a computer-only runtime
keeps its current compatibility behavior.

The Cua browser dependency set is exact:

```text
start_session, end_session, health_report, set_config,
list_apps, list_windows, launch_app, kill_app, press_key,
get_browser_state, browser_prepare, browser_navigate, browser_click,
browser_type, browser_pointer, browser_dialog, browser_set_input_files,
browser_download
```

The implementation owns this list in one place and uses the same list for
startup validation and fake-driver contract tests.

`computer_use` keeps its public tool contract but receives the shared runtime
instead of starting Cua itself. Existing exported server starters remain
compatibility entry points; `StartTransports` uses internal variants that
accept an already-created runtime so the combined case cannot start two Cua
children.

### Browser backend boundary

Browser selection is explicit:

- empty `browser.backend` or `browser.backend: chromedp` uses the current
  implementation;
- `browser.backend: cua` uses the new adapter; and
- every other value is a configuration error.

The browser tool definitions are separated from backend execution. A common
registration layer owns the seven existing public definitions. The Chromedp
and Cua implementations each satisfy the handler boundary without forcing
their unlike internal state into a broad, lowest-common-denominator browser
interface.

The Cua adapter owns:

- prepared browser PID and native window ID;
- opaque Cua target capability;
- current browser connection generation;
- known Cua tabs and compact nib tab aliases;
- the logically selected tab;
- the current semantic snapshot's element aliases and action capabilities;
- the last editable ref used by `browser_type`; and
- initialization and action serialization.

All browser state is scoped to the declared Cua session. A rebind or connection
generation change clears tab aliases, element aliases, and the last editable
ref before any further mutation is accepted.

## Configuration

### Shared Cua settings

Add a top-level Cua configuration:

```yaml
cua:
  command: cua-driver
  args: [mcp]
  env: {}
  session_id: ""
```

The corresponding `types.CUAConfig` holds `Command`, `Args`, `Env`, and
`SessionID`. An empty command resolves through `NIB_CUA_DRIVER_CMD`, then to
`cua-driver`. Empty args resolve to `["mcp"]`. An empty session ID causes nib
to mint one stable ID for the runtime lifetime.

The existing `ComputerConfig.Command`, `Args`, `Env`, and `SessionID` fields
remain deprecated compatibility fallbacks for embedders. A non-empty
top-level `CUAConfig` field wins over its legacy equivalent. New code and
documentation use only `Config.CUA`.

### Browser settings

The browser configuration becomes:

```yaml
browser:
  enabled: true
  backend: cua
  chrome_path: ""
  profile_name: nib
  allow_private_urls: false
```

- `Backend` accepts `chromedp` or `cua`; empty means `chromedp`.
- `ProfileName` defaults to `nib` and must satisfy Cua's path-safe
  `isolated_named` profile-name rules.
- `ChromePath` retains its current meaning and, for Cua, is the preferred
  source-browser launch path.
- `AllowPrivateURLs` keeps its current behavior for both backends.
- `ProfileDir` remains valid for Chromedp. Setting it with the Cua backend is a
  startup error that directs the user to `profile_name`. nib never derives a
  profile name from the path or implies that cookies were migrated.

## Public browser tools

### Existing tools

The Cua backend preserves these existing tools and schemas:

| nib tool | Cua route |
| --- | --- |
| `browser_navigate` | `browser_navigate`, then semantic snapshot |
| `browser_snapshot` | `get_browser_state` with `semantic_v2` |
| `browser_click` | `browser_click`, then semantic snapshot |
| `browser_type` | `browser_type` with `replace=true`, then snapshot |
| `browser_press` | exact-ref Enter or guarded native key, then snapshot |
| `browser_scroll` | `browser_pointer` with `action=scroll`, then snapshot |
| `browser_vision` | exact-tab `get_browser_state(include_screenshot=true)` |

The adapter renders a compact text snapshot and assigns `@eN` aliases to the
Cua refs it exposes. The private map stores each alias's opaque Cua ref and its
declared action capabilities. A fresh snapshot replaces the map, so current
stale-ref behavior remains visible and actionable to the model.

`browser_snapshot(full=true)` consumes semantic continuations until the
existing nib snapshot-character limit is reached or Cua reports the snapshot
complete. It reports truncation or incompleteness explicitly. The default
snapshot returns the first ranked semantic page within the same character
limit.

Mutating existing tools automatically take and return a fresh snapshot. This
keeps the model loop stable even though Cua's native API separates mutation
from verification.

### New tools

New tools are registered only by the Cua backend. Each has a focused input
schema rather than adding fields to the seven existing schemas.

#### `browser_tabs`

Refresh the exact target's tab list and return compact aliases (`@t1`, `@t2`,
...), title, URL, Cua's proven active state, and whether the tab is nib's
logical selection. Refreshing the list does not activate a native tab and does
not invalidate page refs unless the browser binding generation changed.

#### `browser_select_tab`

Accept one current `@tN` alias, change nib's logical target without activating
the tab, and return a fresh semantic snapshot. Selection replaces the element
alias map and last editable ref.

#### `browser_pointer`

Expose Cua's `hover`, `right_click`, `double_click`, `scroll`, and `drag`
actions. Inputs support an origin ref or viewport coordinates, optional drag
destination ref or coordinates, scroll deltas, and the explicit trusted or
DOM-event route. Ref-based actions validate the action declared by the latest
semantic snapshot before calling Cua. The tool refreshes the snapshot after a
successful action.

#### `browser_dialog`

Expose `inspect`, `accept`, and `dismiss` for page-owned JavaScript dialogs.
Inspection is read-only and returns Cua's opaque dialog generation. Acceptance
and dismissal require that generation, preserve Cua's delivery-mode contract,
and refresh the page snapshot. Browser permission UI, file pickers, and native
dialogs remain outside this tool.

#### `browser_set_input_files`

Accept an actionable file-input `@eN` ref and one to 32 paths relative to
`WorkingDir`. Resolve and validate the paths before translating the element
ref and calling Cua. Refresh the semantic snapshot after success.

#### `browser_download`

Accept a live downloadable `@eN` ref and an optional destination directory
relative to `WorkingDir`; the default is `.`. The destination must already
exist. Resolve it to a canonical absolute directory before calling Cua. Return
Cua's path-free success or refusal and a refreshed page snapshot; do not
invent or disclose a filename or destination path that Cua intentionally
withholds.

## Named-key behavior

Cua 0.19 has no typed exact-tab named-key browser tool, so `browser_press`
uses a fail-closed hybrid:

1. After `browser_type`, pressing Enter calls Cua `browser_type` on the cached
   editable ref with `mode="keystrokes"`, `text="\n"`, and `replace=false`.
   Cua maps the newline to an Enter CDP key sequence while retaining exact-tab
   binding, including for an inactive tab.
2. Other currently allowed key names call native `press_key` only when the
   latest exact browser state proves the logically selected tab is active.
3. Enter without a live cached editable ref follows the same guarded native
   route as other keys.
4. If active-tab identity is false or ambiguous, the tool refuses before
   native delivery and tells the model to select/click a page control instead.

The existing key allowlist remains unchanged. No key action silently activates
or substitutes a different tab.

## Browser initialization and lifecycle

The first `browser_navigate` initializes the Cua browser. Other browser calls
before successful initialization return the existing "call browser_navigate
first" guidance. Read-only calls never launch or prepare a browser as a hidden
side effect.

Initialization is serialized:

1. Discover a supported Chrome, Chromium, or Edge source, honoring
   `browser.chrome_path` when set.
2. Prefer a suitable running source process and exact native window.
3. If none exists, call Cua `launch_app`, record the launched PID/window as
   nib-owned temporary state, and wait for its window.
4. Call `browser_prepare` with `allow_launch=true` and
   `profile={mode:"isolated_named", name:<profile_name>}`.
5. Read `prepared_pid`, wait for its native window, and bind it exactly through
   `get_browser_state(pid, window_id, session)`.
6. If the prepared target contains exactly one tab, select it. Otherwise select
   the uniquely proven active tab. Ambiguity is a refusal, not a list-order
   guess.
7. Call Cua `kill_app` for the source only when launch provenance proves nib
   launched that blank temporary process during this initialization. Cua
   0.19.1 exposes no cooperative window-close tool, so the adapter must not
   fake one with unscoped native input. Never terminate a pre-existing source
   process. Cleanup failure is reported but does not erase an otherwise valid
   prepared binding.
8. Navigate the selected prepared tab and return its semantic snapshot.

Preparation has a 60-second timeout. Ordinary browser actions retain the
existing 30-second timeout. Context cancellation aborts waits and calls.

On nib session teardown, the runtime calls Cua `end_session`, closes its MCP
session, and reaps the MCP child. Cua owns cleanup of the prepared process;
the named profile persists for login reuse across later nib sessions.

## Failure and retry contract

- A selected Cua backend never silently falls back to Chromedp.
- Cua structured refusals remain refusals. The adapter preserves refusal code,
  reason, and safe structured details while adding nib-specific recovery
  guidance where useful.
- Transport/protocol errors and structured refusals are distinct.
- A read-only operation may clear stale capabilities, rebind the exact prepared
  PID/window once, and retry once.
- A mutating operation is never retried automatically. This prevents duplicate
  clicks, typing, pointer actions, uploads, downloads, dialog resolution, and
  navigation side effects.
- A connection generation change invalidates target, tab, dialog, snapshot,
  element, and editable-ref state before returning an error or retrying a read.
- Missing screenshots, malformed structured content, capability mismatches,
  ambiguous windows/tabs, and stale aliases fail closed.
- Initialization failure closes only temporary resources proven to be owned by
  the adapter and leaves existing applications untouched.

## Security boundaries

### Navigation

Run the existing `checkURLAllowed` policy before every Cua navigation. The nib
surface continues to accept only HTTP and HTTPS even though Cua also accepts
`about:`. Localhost, RFC1918, link-local, unspecified, `.local`, and supported
numeric-encoding evasions remain blocked unless `allow_private_urls` is true.

### Filesystem

Upload and download paths are confined to the canonical `WorkingDir`:

- relative input is joined to `WorkingDir`;
- absolute input is rejected from the public schema/handler;
- canonicalization occurs before the Cua call;
- symlinks and canonical paths that escape the root are rejected;
- uploads require existing regular files; and
- download destinations require existing directories.

The adapter does not create directories, follow symlinks, or broaden access
based solely on outer approval.

### Profiles and processes

- Only driver-managed `isolated_named` profiles are supported.
- Existing-profile attachment and its grants are not exposed.
- Existing browsers are source descriptors only and are never closed,
  restarted, copied, or modified.
- A temporary source browser is terminated with `kill_app` only when launch
  provenance proves nib created that blank process during the current
  initialization.
- Cua's exact process/window/endpoint proof and structured refusal behavior
  remain authoritative.

### Approval and environment

All new mutating tools remain ordinary nib MCP tools and therefore pass through
nib's existing outer approval policy. Cua's destructive-tool marker and
internal policy remain defense in depth, not a replacement for nib approval.

The shared Cua environment scrubber removes OpenAI, Anthropic, and LocalAI API
keys from inherited environment variables before applying explicitly supplied
`cua.env` entries. Telemetry remains disabled by default, and existing
Wayland-specific environment handling is retained.

## Code organization

The implementation should keep units focused:

- `mcp/cua_runtime.go`: shared Cua child/client/session lifecycle, version and
  tool validation, configuration, and environment handling.
- `mcp/cua_runtime_test.go`: fake-session validation and lifecycle tests.
- `mcp/browser_tools.go`: common seven-tool schemas and backend-neutral
  registration.
- `mcp/browser_cua.go`: lazy initialization, exact binding, state transitions,
  and handler translation.
- `mcp/browser_cua_results.go`: Cua result/refusal parsing, semantic rendering,
  continuation handling, and element/tab aliases.
- `mcp/browser_cua_files.go`: canonical WorkingDir path validation.
- Focused corresponding `_test.go` files for each unit.
- Existing browser files retain the Chromedp implementation and adopt only the
  common registration seam.
- `mcp/computer.go` accepts the shared runtime rather than privately starting
  Cua.
- `mcp/transport.go` resolves and injects one runtime.
- `types/config.go` and config tests define the new settings and compatibility
  precedence.
- `README.md` documents selection, migration, tools, requirements, and live
  testing.

Exact file splits may follow existing package conventions, but result parsing,
filesystem policy, and runtime ownership must not collapse into one large
browser implementation file.

## Testing

Implementation is test-driven. Normal tests use a configurable fake Cua MCP
server/session and do not require Cua Driver, Chromium, a display, or network
access.

### Hermetic coverage

Tests must prove:

- computer-only, browser-only, and combined enablement create the correct
  number of Cua runtimes; combined enablement creates exactly one;
- new Cua settings win and legacy Computer fields remain fallbacks;
- invalid backend/profile configuration fails before transport startup;
- missing, old, or incomplete Cua installations fail synchronously;
- lazy source reuse/launch, preparation, binding, and owned cleanup occur in
  the required order;
- pre-existing source browsers are never closed;
- semantic refs become `@eN`, tab identities become `@tN`, action
  capabilities are enforced, and invalidation boundaries are correct;
- every existing and new nib handler generates the exact expected Cua call;
- successful mutations refresh the snapshot;
- mutations are never retried and stale reads rebind at most once;
- the key hybrid uses exact-ref Enter where possible and refuses unsafe native
  delivery;
- Cua refusals and transport errors remain distinguishable;
- URL policy rejects unsafe navigation before driver invocation;
- upload/download paths cannot escape `WorkingDir` through absolute paths,
  traversal, or symlinks;
- named-profile validation and `profile_dir` rejection are actionable; and
- all existing Chromedp unit and integration tests remain valid.

### Live coverage

Add opt-in tests behind the `cuabrowserintegration` build tag. They require a
real Cua Driver 0.19.0 or newer, a supported Chromium installation, and a
usable display. Against a local HTTP fixture, cover:

- prepare/bind/navigate and semantic snapshot;
- replacement typing and Enter form submission;
- pointer delivery and post-action verification;
- exact-tab screenshot;
- creation/discovery and logical selection of a second tab;
- file-input assignment from a temporary `WorkingDir` file;
- JavaScript dialog inspect and resolution; and
- a download confined to a temporary `WorkingDir` directory.

The suite must use a unique named profile/session, avoid personal profiles,
and clean up processes it owns. It is documented for maintainers but is not
part of ordinary GitHub Actions in this release.

### Completion gate

Run the repository-wide gate serially because browser/computer packages can
contend over GUI resources when package tests run concurrently:

```bash
go build ./...
go test -p 1 ./...
```

Run the opt-in Cua integration suite separately on a suitable host and record
its result in the implementation handoff.

## Rollout

This release ships Cua as an explicit opt-in. Documentation presents it as a
new backend, not as a transparent replacement or default. Chromedp remains the
fallback a user selects explicitly by leaving the backend empty or setting it
to `chromedp`.

A later design may make Cua the default only after real-world parity evidence
covers persistent login, common form flows, key handling, tab management,
profile cleanup, and all supported platforms. Removing Chromedp is a separate
decision and is not implied by this work.

## Rejected alternatives

### Independent Cua process for the browser

This minimizes changes to `computer_use` but duplicates process lifecycle,
health checks, environment policy, and sessions. It also weakens Cua's core
value: one browser/native session. Rejected in favor of shared ownership.

### Raw Cua browser tools alongside nib tools

This avoids translation but exposes setup, target IDs, tab IDs, sessions,
snapshot formats, continuations, and route choices directly to local models.
It duplicates concepts and abandons nib's compact post-action loop. Rejected.

### Broad common backend interface before implementation

Chromedp's implicit single page and Cua's exact process/window/target/tab
capabilities do not share a useful large interface. A broad abstraction would
either leak Cua details into Chromedp or erase useful Cua semantics. Rejected
in favor of common tool registration plus backend-specific state.

### Immediate default switch or Chromedp removal

Cua's browser surface is new, depends on an external binary, and lacks a typed
exact-tab named-key action. An opt-in rollout provides evidence without
breaking existing browser users. Immediate replacement is rejected.

### Existing-profile support

Attaching a personal profile carries materially broader CDP authority and
requires a separate trusted-grant design. The current nib browser already uses
a dedicated profile, so `isolated_named` preserves the intended login-once
workflow without expanding authority. Existing-profile support is rejected for
this phase.

## Open questions

None. Backend rollout, tool scope, profile strategy, key behavior, runtime
sharing, tab API, startup lifecycle, configuration migration, filesystem
scope, failure behavior, and test scope are decided above.
