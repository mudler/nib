# Cua Browser Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an explicit `browser.backend: cua` implementation that preserves nib's seven compact browser tools, adds the selected Cua-only browser capabilities, and shares one declared Cua session with `computer_use` while keeping Chromedp as the default.

**Architecture:** Extract the existing browser tool registration from the Chromedp implementation, introduce a session-owning `cuaRuntime`, and inject that runtime into both computer and Cua-browser adapters. The Cua browser adapter remains lazy until the first navigation, binds one driver-managed `isolated_named` Chromium profile to an exact process/window/target, translates opaque Cua refs and tabs into nib aliases, and treats structured refusals separately from transport failures.

**Tech Stack:** Go 1.26, `github.com/modelcontextprotocol/go-sdk` v1.0.0, existing `github.com/Masterminds/semver/v3`, existing Chromedp backend, Cua Driver MCP 0.19.0+.

## Global Constraints

- The approved design is `docs/superpowers/specs/2026-08-07-cua-browser-backend-design.md`. If code and this plan disagree with that document, stop and reconcile the documents before changing behavior.
- Use `superpowers:test-driven-development` for every production change: add the focused failing test, run it and observe the intended failure, write only enough implementation, then rerun the focused test.
- Empty `browser.backend` and `chromedp` select the existing implementation. `cua` is opt-in. Never fall back from a requested Cua backend to Chromedp.
- Keep all seven existing browser tool names and `BrowserInput` fields unchanged. Cua-only features get separate tools and separate input structs.
- Cua 0.19 refusals are successful MCP calls with `StructuredContent.status == "refused"`; they are not transport errors and normally have `IsError == false`. Preserve `refusal.code`, `refusal.message`, and safe `refusal.detail`.
- The semantic-v2 result contract used here is: top-level `outline`, `refs`, `content_refs`, `page`, `snapshot`, `target_id`, and `tab_id`; each actionable ref has `ref`, `role`, `name`, `value`, `states`, `actions`, `frame`, and `visibility`; `snapshot.continuation` is opaque and single-use. This was verified against the `cua-driver-rs-v0.19.0` source tag.
- Treat a changed opaque Cua `target_id` as the observable browser connection-generation boundary. Clear tabs, refs, dialog state, and cached editable ref before using the new target.
- Never retry a mutating Cua call. A read may rebind and retry once only for a stale binding/generation refusal.
- Public upload/download inputs are relative to canonical `Config.WorkingDir`. Reject absolute paths, traversal, every symlink component, non-regular uploads, missing download directories, and more than 32 uploads before Cua sees the call.
- Only `profile.mode="isolated_named"` is emitted. Profile names are 1–64 ASCII letters, digits, `-`, or `_`; empty defaults to `nib`. `browser.profile_dir` with the Cua backend is an error.
- Do not expose raw Cua refs, target IDs, tab IDs, continuation tokens, source URLs, filenames, or canonical local paths in nib's public structured output or user-facing summaries. Except for validated screenshot image blocks, discard Cua's raw text/content and construct nib-owned browser results from sanitized structured data.
- Preserve the current Cua child environment policy: provider API keys removed, telemetry disabled, native Wayland preference retained, explicit `cua.env` applied last.
- Do not touch the existing untracked `.worktrees/`, `cmd/e2e*`, or root `e2e*` artifacts. They belong to the user and are outside this work.
- Run repository-wide tests serially. Package-parallel browser/computer tests may contend over GUI resources.

---

### Task 1: Add Cua and backend configuration

**Files:**
- Modify: `types/config.go`
- Modify: `config/overrides_test.go`
- Create: `mcp/browser_config.go`
- Create: `mcp/browser_config_test.go`

**Interfaces:**
- Produces `types.CUAConfig` and `types.Config.CUA`.
- Extends `types.BrowserConfig` with `Backend` and `ProfileName` without changing existing fields.
- Produces `resolveCUAConfig`, `browserBackend`, and `cuaProfileName` for later tasks.

- [ ] **Step 1: Write failing YAML and resolution tests**

Add tests which load this file through `config.LoadWith` and compare every field:

```go
cua:
  command: /opt/cua-driver
  args: [mcp, --embedded]
  env:
    CUA_SOCKET: /tmp/cua.sock
  session_id: run-7
browser:
  enabled: true
  backend: cua
  profile_name: nib-ci
```

In `mcp/browser_config_test.go`, cover:

```go
func TestResolveCUAConfigUsesTopLevelFieldsThenLegacyFallbacks(t *testing.T)
func TestBrowserBackendDefaultsToChromedp(t *testing.T)
func TestBrowserBackendRejectsUnknownValue(t *testing.T)
func TestCuaBrowserRejectsProfileDir(t *testing.T)
func TestCuaProfileNameDefaultsAndValidates(t *testing.T)
```

The precedence test must mix the two sources field-by-field, for example top-level `Command` and `Env` with legacy `Args` and `SessionID`, and assert that a non-empty top-level field wins independently.

- [ ] **Step 2: Run the focused tests and verify failure**

Run: `go test ./config ./mcp -run 'CUAConfig|ResolveCUA|BrowserBackend|CuaProfile' -v`

Expected: FAIL because `CUAConfig`, browser backend fields, and helpers do not exist.

- [ ] **Step 3: Add the configuration types**

In `types/config.go`, add:

```go
// CUAConfig configures the shared cua-driver MCP child used by computer_use
// and by browser.backend=cua.
type CUAConfig struct {
	Command   string            `yaml:"command,omitempty"`
	Args      []string          `yaml:"args,omitempty"`
	Env       map[string]string `yaml:"env,omitempty"`
	SessionID string            `yaml:"session_id,omitempty"`
}
```

Add `CUA CUAConfig \`yaml:"cua,omitempty"\`` beside `Computer`, and extend `BrowserConfig` with:

```go
	Backend     string `yaml:"backend,omitempty"`      // ""/chromedp or cua
	ProfileName string `yaml:"profile_name,omitempty"` // cua isolated_named profile; "" = nib
```

Update the surrounding comments: `Computer.Command/Args/Env/SessionID` are deprecated embedder fallbacks; the browser may be Chromedp- or Cua-backed.

- [ ] **Step 4: Implement normalization and validation**

Create `mcp/browser_config.go` with these exact boundaries:

```go
const defaultCUAProfileName = "nib"

func resolveCUAConfig(cfg types.Config) types.CUAConfig
func browserBackend(cfg types.BrowserConfig) (string, error)
func cuaProfileName(cfg types.BrowserConfig) (string, error)
func validateBrowserConfig(cfg types.BrowserConfig) error
```

`resolveCUAConfig` applies non-empty top-level fields first, then the matching legacy Computer field, then `NIB_CUA_DRIVER_CMD`/`cua-driver` for command and `[]string{"mcp"}` for args. Clone slices/maps so later mutation cannot alias caller configuration. Session minting belongs to the runtime, not this resolver.

`browserBackend` trims/lowercases only for comparison and returns canonical `chromedp` or `cua`. `cuaProfileName` implements the Cua 0.19 rule exactly: `^[A-Za-z0-9_-]{1,64}$`.

- [ ] **Step 5: Make the exhaustive config tests include the new fields**

`fillNonZero` already walks structs recursively. Add explicit assertions for YAML tags and per-field map/slice precedence so a future tag or merge regression cannot hide inside the generic comparison.

- [ ] **Step 6: Run tests and commit**

Run: `gofmt -w types/config.go mcp/browser_config.go mcp/browser_config_test.go config/overrides_test.go && go test ./types ./config ./mcp -run 'Config|ResolveCUA|BrowserBackend|CuaProfile' -v`

Expected: PASS.

```bash
git add types/config.go config/overrides_test.go mcp/browser_config.go mcp/browser_config_test.go
git commit -m "feat(config): add the opt-in Cua browser backend"
```

---

### Task 2: Build reusable fake Cua test infrastructure

**Files:**
- Create: `mcp/cua_fake_test.go`
- Modify: `mcp/computer_test.go`

**Interfaces:**
- Produces `fakeCUADriver`, `fakeCUACall`, `startFakeCUA`, `cuaOK`, and `cuaRefused` for all later unit tests.
- Keeps existing computer tests hermetic while making tool rosters, versions, responses, and call order configurable.

- [ ] **Step 1: Write the helper contract as a failing compile-time use**

Add one small test that starts a fake at version `0.19.0`, registers a `health_report` response, calls it, and asserts the recorded name/arguments. The helper shape is:

```go
type fakeCUACall struct {
	Name string
	Args map[string]any
}

type fakeCUAHandler func(map[string]any) (*mcp.CallToolResult, map[string]any, error)

type fakeCUADriver struct {
	Session *mcp.ClientSession
	mu      sync.Mutex
	calls   []fakeCUACall
}

func startFakeCUA(
	t *testing.T,
	ctx context.Context,
	version string,
	toolNames []string,
	handlers map[string]fakeCUAHandler,
) *fakeCUADriver

func (f *fakeCUADriver) Calls() []fakeCUACall
func cuaOK(structured map[string]any) (*mcp.CallToolResult, map[string]any, error)
func cuaRefused(code, message string, detail map[string]any) (*mcp.CallToolResult, map[string]any, error)
```

Register every requested tool on an in-memory MCP server. An absent custom handler returns `status:"ok"`. Use the typed handler output for structured data because go-sdk v1.0.0 overwrites `CallToolResult.StructuredContent` during typed handler serialization.

- [ ] **Step 2: Observe the compile failure, then implement the helper**

Run: `go test ./mcp -run FakeCUA -v`

Expected before implementation: FAIL with undefined helper symbols. After implementation: PASS.

- [ ] **Step 3: Replace the private computer fake with the shared helper**

Convert `startFakeDriver` in `mcp/computer_test.go` into a thin fixture builder around `startFakeCUA`. Keep all existing responses and assertions unchanged. The production constructor still accepts `*mcp.ClientSession` at this point.

- [ ] **Step 4: Run existing computer tests and commit**

Run: `gofmt -w mcp/cua_fake_test.go mcp/computer_test.go && go test ./mcp -run 'FakeCUA|Computer' -v`

Expected: PASS.

```bash
git add mcp/cua_fake_test.go mcp/computer_test.go
git commit -m "test(mcp): add a configurable fake Cua driver"
```

---

### Task 3: Introduce the shared Cua runtime

**Files:**
- Create: `mcp/cua_runtime.go`
- Create: `mcp/cua_runtime_test.go`
- Modify: `mcp/computer.go`

**Interfaces:**
- Produces `cuaCaller`, `cuaClient`, `cuaRuntime`, `newCUARuntime`, and `newCUARuntimeFromClient`.
- Moves `scrubbedDriverEnv` out of `computer.go` without changing its behavior.
- Requires the browser dependency roster only when `requireBrowser` is true.

- [ ] **Step 1: Write failing runtime tests**

Cover all of these cases with the fake server:

```go
func TestCUARuntimeStartsAndEndsOneDeclaredSession(t *testing.T)
func TestCUARuntimeMintsStableNonDefaultSession(t *testing.T)
func TestCUARuntimeUsesConfiguredSessionID(t *testing.T)
func TestCUARuntimeRejectsDefaultSessionID(t *testing.T)
func TestCUARuntimeRejectsOldBrowserDriver(t *testing.T)
func TestCUARuntimeRejectsMalformedBrowserDriverVersion(t *testing.T)
func TestCUARuntimeReportsEveryMissingBrowserTool(t *testing.T)
func TestCUARuntimeAllowsOldComputerOnlyDriver(t *testing.T)
func TestCUARuntimeListsAllToolPages(t *testing.T)
func TestScrubbedDriverEnvDropsProviderKeysAndKeepsOverrides(t *testing.T)
```

The lifecycle assertion must prove `start_session` is called once with `session:<id>` before health/config calls and `end_session` is called once during `Close`, even if `Close` is invoked twice. Empty session IDs are minted; an explicit `default` is rejected because Cua browser capabilities require a non-default declared session.

- [ ] **Step 2: Run and verify failure**

Run: `go test ./mcp -run 'CUARuntime|ScrubbedDriverEnv' -v`

Expected: FAIL because the runtime does not exist.

- [ ] **Step 3: Implement the runtime types and browser contract**

Use these interfaces so production uses `*mcp.ClientSession` and tests can still use the in-memory session:

```go
type cuaCaller interface {
	CallTool(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, error)
}

type cuaClient interface {
	cuaCaller
	InitializeResult() *mcp.InitializeResult
	ListTools(context.Context, *mcp.ListToolsParams) (*mcp.ListToolsResult, error)
	Close() error
}

type cuaRuntime struct {
	client    cuaClient
	sessionID string
	connCancel context.CancelFunc
	closeOnce sync.Once
	closeErr  error
}

func newCUARuntime(ctx context.Context, cfg types.Config, requireBrowser bool) (*cuaRuntime, error)
func newCUARuntimeFromClient(ctx context.Context, client cuaClient, cfg types.Config, requireBrowser bool) (*cuaRuntime, error)
func (r *cuaRuntime) CallTool(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, error)
func (r *cuaRuntime) SessionID() string
func (r *cuaRuntime) Close() error
```

Own the dependency list once:

```go
var cuaBrowserRequiredTools = []string{
	"start_session", "end_session", "health_report", "set_config",
	"list_apps", "list_windows", "launch_app", "kill_app", "press_key",
	"get_browser_state", "browser_prepare", "browser_navigate", "browser_click",
	"browser_type", "browser_pointer", "browser_dialog",
	"browser_set_input_files", "browser_download",
}
```

Use `InitializeResult().ServerInfo.Version` with `semver.NewVersion` and require `>= 0.19.0` only for browser mode. Iterate `ListTools` until `NextCursor == ""`, collect all missing names, sort them, and return one actionable error. Use the in-memory fake server for normal cases and a small local `cuaClient` stub for the pagination test so `NextCursor` behavior is exercised deterministically.

Mint an ID like `nib-<24 lowercase hex chars>` with `crypto/rand`. Connect the MCP client with a runtime-owned context derived from `context.Background()`, not directly from the nib session context: on nib cancellation, `Close` must still be able to call `end_session` before `connCancel`. Call `start_session` with `capture_scope:"window"`; treat transport errors or `IsError` as construction failures. On any construction error after connection, close the client and reap the child; if the session already started, end it first. Use a five-second background timeout for normal `end_session`, then cancel the connection context and close the client.

- [ ] **Step 4: Move connection and environment ownership**

Move the command resolution, `exec.Command`, `mcp.NewClient(...).Connect`, `scrubbedDriverEnv`, screenshot `set_config`, and best-effort `health_report` code from `StartComputerMCPServer` into the runtime. Apply screenshot configuration only when computer control is enabled. Preserve the Linux max-dimension exception and all existing log messages.

`set_config` and `health_report` remain best-effort for compatibility; connection, browser validation, and `start_session` are hard failures.

- [ ] **Step 5: Keep computer production code compiling through the caller boundary**

Change only the field/constructor types now:

```go
type computerServer struct {
	driver cuaCaller
	// existing fields unchanged
}

func newComputerServer(driver cuaCaller, cfg types.ComputerConfig) *computerServer
```

Do not yet change transport ownership; that is Task 4.

- [ ] **Step 6: Run tests and commit**

Run: `gofmt -w mcp/cua_runtime.go mcp/cua_runtime_test.go mcp/computer.go && go test ./mcp -run 'CUARuntime|ScrubbedDriverEnv|Computer' -v`

Expected: PASS.

```bash
git add mcp/cua_runtime.go mcp/cua_runtime_test.go mcp/computer.go
git commit -m "refactor(mcp): centralize the Cua runtime lifecycle"
```

---

### Task 4: Share exactly one runtime across transports

**Files:**
- Modify: `mcp/transport.go`
- Modify: `mcp/transport_computer_test.go`
- Modify: `mcp/computer.go`
- Modify: `mcp/browser.go`

**Interfaces:**
- Produces internal runtime-aware starters for computer and browser.
- Keeps exported `StartComputerMCPServer` and `StartBrowserMCPServer` as compatibility entry points.

- [ ] **Step 1: Write failing ownership and startup tests**

Replace the current `/bin/true` computer test with factory injection and add:

```go
func TestStartTransportsComputerOnlyCreatesOneCUARuntime(t *testing.T)
func TestStartTransportsCUABrowserOnlyCreatesOneCUARuntime(t *testing.T)
func TestStartTransportsCombinedCreatesOneSharedCUARuntime(t *testing.T)
func TestStartTransportsChromedpBrowserCreatesNoCUARuntime(t *testing.T)
func TestStartTransportsReturnsCUAConstructionErrorSynchronously(t *testing.T)
func TestStartTransportsRejectsInvalidBrowserConfigBeforeStartingServers(t *testing.T)
```

The combined test must assert one factory call and one `start_session` call. Once the Cua browser starter exists in Task 7, extend the test to connect to both returned transports, exercise each surface, and assert both driver calls carry the same recorded session ID.

- [ ] **Step 2: Run and verify failure**

Run: `go test ./mcp -run StartTransports -v`

Expected: FAIL because each exported starter still owns its lifecycle and no factory seam exists.

- [ ] **Step 3: Add an injected implementation boundary**

Use a private overload rather than a mutable package global:

```go
type cuaRuntimeFactory func(context.Context, types.Config, bool) (*cuaRuntime, error)

func StartTransports(ctx context.Context, cfg types.Config, jobs *ShellJobs) ([]mcp.Transport, error) {
	return startTransports(ctx, cfg, jobs, newCUARuntime)
}

func startTransports(ctx context.Context, cfg types.Config, jobs *ShellJobs, makeCUA cuaRuntimeFactory) ([]mcp.Transport, error)
```

At the top of `startTransports`, validate browser config and synchronously create a runtime when computer is enabled or the selected browser backend is Cua. Do this before starting any server goroutine. Start one cleanup goroutine that waits for `ctx.Done()` and calls `runtime.Close()`.

- [ ] **Step 4: Split compatibility wrappers from injected starters**

Use these internal functions:

```go
func startComputerMCPServer(ctx context.Context, transport mcp.Transport, cfg types.Config, runtime *cuaRuntime) error
func startBrowserMCPServer(ctx context.Context, transport mcp.Transport, cfg types.Config, runtime *cuaRuntime) error
```

The exported computer wrapper creates `newCUARuntime(ctx, cfg, false)`, defers `Close`, and delegates. The internal computer starter copies `cfg.Computer`, overwrites its `SessionID` with `runtime.SessionID()`, and passes the runtime into `newComputerServer`.

The exported browser wrapper validates the backend. Chromedp delegates directly; Cua creates `newCUARuntime(ctx, cfg, true)`, defers `Close`, and delegates to the Cua starter added later. Until Task 7, return a clear `Cua browser backend is not implemented` error from that Cua branch so the tests have an explicit seam.

- [ ] **Step 5: Run tests and commit**

Run: `gofmt -w mcp/transport.go mcp/transport_computer_test.go mcp/computer.go mcp/browser.go && go test ./mcp -run 'StartTransports|Computer' -v`

Expected: PASS.

```bash
git add mcp/transport.go mcp/transport_computer_test.go mcp/computer.go mcp/browser.go
git commit -m "refactor(mcp): share one Cua runtime per nib session"
```

---

### Task 5: Extract backend-neutral browser tool registration

**Files:**
- Create: `mcp/browser_tools.go`
- Create: `mcp/browser_tools_test.go`
- Modify: `mcp/browser.go`
- Modify: `mcp/browser_test.go`

**Interfaces:**
- Produces `browserToolHandlers`, `registerBrowserTools`, and shared result/refusal types.
- Keeps Chromedp handler behavior and public schemas byte-for-byte equivalent.

- [ ] **Step 1: Pin the current seven-tool contract**

Create a test that starts the Chromedp browser MCP server over in-memory transport, lists tools, and asserts exactly these names and the current `BrowserInput` property names/enums:

```text
browser_navigate browser_snapshot browser_click browser_type
browser_press browser_scroll browser_vision
```

Also assert that `browser.backend: cua` is not silently routed through `newBrowserServer`.

- [ ] **Step 2: Run the contract test before refactoring**

Run: `go test ./mcp -run 'BrowserToolContract|BrowserInputSchema' -v`

Expected: the new contract test initially fails to compile; after adding only the test harness against the old server, it passes and records the baseline.

- [ ] **Step 3: Move shared definitions and registration**

Move `BrowserInput`, `BrowserOutput`, `browserInputSchema`, the seven descriptions, and the `addBrowserTool` logic into `mcp/browser_tools.go`. Add refusal metadata without removing existing output fields:

```go
type BrowserRefusal struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Detail  any            `json:"detail,omitempty"`
}

type BrowserOutcome struct {
	Status  string          `json:"status,omitempty"`
	Refusal *BrowserRefusal `json:"refusal,omitempty"`
}

type BrowserOutput struct {
	BrowserOutcome
	Snapshot     string `json:"snapshot,omitempty"`
	ElementCount int    `json:"element_count,omitempty"`
}

type browserToolHandlers struct {
	navigate mcp.ToolHandlerFor[BrowserInput, BrowserOutput]
	snapshot mcp.ToolHandlerFor[BrowserInput, BrowserOutput]
	click    mcp.ToolHandlerFor[BrowserInput, BrowserOutput]
	typeText mcp.ToolHandlerFor[BrowserInput, BrowserOutput]
	press    mcp.ToolHandlerFor[BrowserInput, BrowserOutput]
	scroll   mcp.ToolHandlerFor[BrowserInput, BrowserOutput]
	vision   mcp.ToolHandlerFor[BrowserInput, BrowserOutput]
}

func registerBrowserTools(server *mcp.Server, handlers browserToolHandlers)
```

The anonymous `BrowserOutcome` embedding flattens status/refusal in JSON. Chromedp continues returning empty outcome fields.

- [ ] **Step 4: Make the Chromedp starter use the shared registration**

Rename its private execution path to `startChromedpBrowserMCPServer`, keep `browserServer` and every handler intact, and pass its bound methods to `registerBrowserTools`.

- [ ] **Step 5: Prove no Chromedp regression and commit**

Run: `gofmt -w mcp/browser_tools.go mcp/browser_tools_test.go mcp/browser.go mcp/browser_test.go && go test ./mcp -run Browser -v`

Expected: PASS, including all current URL, ref, key, snapshot, and schema tests.

```bash
git add mcp/browser_tools.go mcp/browser_tools_test.go mcp/browser.go mcp/browser_test.go
git commit -m "refactor(browser): share tool registration across backends"
```

---

### Task 6: Parse Cua results and render stable nib aliases

**Files:**
- Create: `mcp/browser_cua_results.go`
- Create: `mcp/browser_cua_results_test.go`

**Interfaces:**
- Produces typed Cua envelopes, `decodeCUAResult`, `renderCUASnapshot`, and alias-state helpers.
- Makes structured refusals a first-class non-error result.

- [ ] **Step 1: Write table-driven parser tests with literal Cua 0.19 fixtures**

Cover:

- bind results with one, multiple, true/false/null-active tabs;
- semantic snapshots with action refs, content refs, omission counts, and continuation;
- screenshot content plus viewport metadata;
- `status:"refused"` with and without detail;
- `IsError:true`, malformed/missing structured content, and wrong field types;
- changed target ID invalidating every alias cache;
- deterministic `@tN` and fresh-per-snapshot `@eN` allocation;
- no raw target/tab/ref/continuation token appearing in rendered output;
- prefix-colliding refs such as `p1:1` and `p1:10` replaced longest-first without corrupting either alias;
- refs whose lines are removed by truncation are absent from the installed alias map unless their alias still appears in the retained outline;
- truncation at `maxSnapshotChars` on a line boundary.

- [ ] **Step 2: Run and verify failure**

Run: `go test ./mcp -run 'CUAResult|CUASnapshot|CUAAlias|CUARefusal' -v`

Expected: FAIL because result parsing does not exist.

- [ ] **Step 3: Add exact internal result types**

Use internal JSON types, not `map[string]any`, after the decode boundary:

```go
type cuaRefusal struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Detail  any            `json:"detail,omitempty"`
}

type cuaTab struct {
	ID     string `json:"tab_id"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Active *bool  `json:"active"`
}

type cuaSemanticRef struct {
	Ref        string   `json:"ref"`
	Role       string   `json:"role"`
	Name       string   `json:"name"`
	Value      string   `json:"value"`
	States     map[string]any `json:"states"`
	Actions    []string `json:"actions"`
	Frame      string   `json:"frame"`
	Visibility string   `json:"visibility"`
}

type cuaResultEnvelope struct {
	Status          string            `json:"status"`
	Mode            string            `json:"mode"`
	TargetID        string            `json:"target_id"`
	BindingQuality  string            `json:"binding_quality"`
	MutationAllowed bool              `json:"mutation_allowed"`
	Tabs            []cuaTab          `json:"tabs"`
	Snapshot        cuaSnapshotMeta   `json:"snapshot"`
	Page            cuaPage           `json:"page"`
	Outline         string            `json:"outline"`
	Refs            []cuaSemanticRef  `json:"refs"`
	ContentRefs     []cuaSemanticRef  `json:"content_refs"`
	Refusal         *cuaRefusal       `json:"refusal"`
	Screenshot      *cuaScreenshot    `json:"screenshot"`
}
```

Include result-specific optional fields used later (`prepared_pid`, `pid`, `windows`, `dialog_id`, `kind`, `present`, `download_id`, `bytes`, and assigned count) either in the envelope or small dedicated structs.

`decodeCUAResult` JSON-marshals `StructuredContent` then unmarshals into the requested typed value. It treats only `status:"refused"` as a refusal—valid success statuses include `ok` and `completed`—and returns three distinct outcomes: decoded success, decoded refusal, or Go error for transport/protocol/malformed data. Add a browser-state-aware `publicRefusal` conversion which replaces known raw refs/tabs/target IDs and sensitive string arguments (`files`, `destination_root`) in the message with nib aliases/redaction, and recursively drops detail keys containing `target`, `tab`, `ref`, `continuation`, `endpoint`, `path`, `url`, or `filename`; tests must prove capability and filesystem values cannot escape through refusal text.

- [ ] **Step 4: Implement alias rendering**

`renderCUASnapshot` must:

1. concatenate the semantic outline and one compact line per actionable ref;
2. allocate `@e1`, `@e2`, ... in response order;
3. replace any raw ref occurrence in the outline longest-first before returning it;
4. after truncation, scan retained text with the exact `@e\d+` token matcher and install raw refs/actions only for aliases the model actually received;
5. ignore `content_refs` for action aliases;
6. append an explicit incomplete/truncated marker based on `snapshot.complete`, continuation, omissions, and nib's character limit; and
7. return `BrowserOutput{Status:"ok", Snapshot:..., ElementCount:len(refs)}`.

- [ ] **Step 5: Run tests and commit**

Run: `gofmt -w mcp/browser_cua_results.go mcp/browser_cua_results_test.go && go test ./mcp -run 'CUAResult|CUASnapshot|CUAAlias|CUARefusal' -v`

Expected: PASS.

```bash
git add mcp/browser_cua_results.go mcp/browser_cua_results_test.go
git commit -m "feat(browser): parse Cua semantic browser state"
```

---

### Task 7: Implement lazy prepare, exact binding, and logical tabs

**Files:**
- Create: `mcp/browser_cua.go`
- Create: `mcp/browser_cua_test.go`
- Modify: `mcp/browser.go`

**Interfaces:**
- Produces `cuaBrowserServer`, `newCUABrowserServer`, lazy initialization, exact rebinding, and Cua browser server startup.
- Implements `browser_tabs` and `browser_select_tab` first, before page mutations.

- [ ] **Step 1: Write failing lifecycle/state-machine tests**

Use scripted fake responses and assert exact ordered calls for:

```go
func TestCUABrowserOtherToolsRequireNavigateFirst(t *testing.T)
func TestCUABrowserNavigateReusesRunningSource(t *testing.T)
func TestCUABrowserNavigateLaunchesAndKillsOnlyOwnedTemporarySource(t *testing.T)
func TestCUABrowserNeverKillsPreexistingSource(t *testing.T)
func TestCUABrowserPrepareUsesIsolatedNamedProfile(t *testing.T)
func TestCUABrowserPrepareTimesOutAfterConfiguredBound(t *testing.T)
func TestCUABrowserSelectsOnlyTabOrUniqueActiveTab(t *testing.T)
func TestCUABrowserRefusesAmbiguousInitialTabs(t *testing.T)
func TestCUABrowserTargetChangeInvalidatesCapabilities(t *testing.T)
func TestCUABrowserTabsPreserveAliasesWithoutActivating(t *testing.T)
func TestCUABrowserSelectTabIsLogicalOnly(t *testing.T)
func TestStartTransportsCombinedCUASurfacesUseOneSessionID(t *testing.T)
func TestCUABrowserInitializationFailureNeverFallsBackToChromedp(t *testing.T)
```

Inject timeouts through package variables or a clock option in tests; never wait 60 real seconds.

- [ ] **Step 2: Run and verify failure**

Run: `go test ./mcp -run 'CUABrowser.*(Navigate|Prepare|Source|Tab|Capabilities)' -v`

Expected: FAIL because the Cua adapter does not exist.

- [ ] **Step 3: Add the serialized state holder**

Start with this shape:

```go
const cuaBrowserPrepareTimeout = 60 * time.Second

type cuaBrowserServer struct {
	runtime    *cuaRuntime
	cfg        types.Config
	actionMu   sync.Mutex
	mu         sync.Mutex
	preparedPID int
	windowID    int
	targetID    string
	tabs        map[string]cuaTab       // raw id -> state
	tabAliases  map[string]string       // @tN -> raw id
	tabReverse  map[string]string       // raw id -> @tN
	nextTabAlias int
	selectedTab string                  // raw id
	refs        map[string]cuaElement   // @eN -> raw capability/actions
	lastEditable string                 // raw ref from latest snapshot
	dialogID    string
	cleanupWarning string               // safe one-shot owned-source cleanup warning
}

type cuaElement struct {
	Raw     string
	Actions map[string]bool
}
```

`actionMu` covers every multi-call browser operation. `mu` protects state snapshots but is never held during a driver call.

- [ ] **Step 4: Implement source selection and preparation**

Initialization occurs only inside the first `browserNavigate` call:

1. call `list_apps` and `list_windows`;
2. select only Chrome, Chromium, or Edge; if `ChromePath` is set, require a matching `launch_path`/executable identity rather than substituting another browser;
3. prefer a running candidate with one exact native window;
4. otherwise call `launch_app` using the round-trippable `launch_path`, Windows `path`, bundle ID, or name from `list_apps`, and record the returned PID as owned;
5. poll `list_windows` under the 60-second preparation context until one window for the source PID exists;
6. call `browser_prepare` with `pid`, `session`, `allow_launch:true`, and `profile:{mode:"isolated_named",name:<validated name>}`;
7. require a positive `prepared_pid`, poll its exact window, then bind with `get_browser_state(pid,window_id,session)`;
8. require `binding_quality:"exact"` and `mutation_allowed:true`;
9. choose the sole tab or uniquely `active:true` tab; reject ambiguity; and
10. call `kill_app` only for the temporary source PID recorded by this initialization. Report cleanup failure without discarding a valid prepared binding.

Carry the runtime session on every source/prepare/bind/cleanup call. Install a deferred failure cleanup immediately after an owned source launch so an error in any later initialization step still kills that source. Never kill a pre-existing PID, and never kill the source if Cua reports it as the `prepared_pid`; in that case session teardown owns it. If cleanup fails after a valid bind, retain a sanitized one-shot warning and include it with the first navigation result instead of discarding the binding. Never pass remote-debugging flags or private approval-marker arguments through `launch_app`/`browser_prepare`; the Cua MCP proxy owns approval and preparation owns endpoint setup.

- [ ] **Step 5: Implement tab refresh/selection and Cua-only registration**

Add focused inputs/outputs:

```go
type BrowserTabsInput struct{}
type BrowserSelectTabInput struct {
	TabID string `json:"tab_id" jsonschema:"current tab alias, e.g. @t2"`
}
type BrowserTabOutput struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Active   *bool  `json:"active"`
	Selected bool   `json:"selected"`
}
type BrowserTabsOutput struct {
	BrowserOutcome
	Tabs []BrowserTabOutput `json:"tabs,omitempty"`
}
```

`browser_tabs` re-runs bind on the exact prepared PID/window, applies generation invalidation only if target changes, and maintains stable aliases for surviving raw tabs. `browser_select_tab` accepts only a current alias, changes `selectedTab` without native input, clears element/editable/dialog state, and returns a semantic snapshot.

Register the seven shared handlers plus Cua-only tools in `startCUABrowserMCPServer`. Replace Task 4's placeholder Cua error with this starter.

- [ ] **Step 6: Run tests and commit**

Run: `gofmt -w mcp/browser_cua.go mcp/browser_cua_test.go mcp/browser.go && go test ./mcp -run 'CUABrowser|StartTransports' -v`

Expected: PASS.

```bash
git add mcp/browser_cua.go mcp/browser_cua_test.go mcp/browser.go
git commit -m "feat(browser): prepare and bind a Cua browser session"
```

---

### Task 8: Map the seven existing browser tools to Cua

**Files:**
- Modify: `mcp/browser_cua.go`
- Modify: `mcp/browser_cua_results.go`
- Create: `mcp/browser_cua_actions_test.go`

**Interfaces:**
- Completes all seven `browserToolHandlers` for the Cua backend.
- Produces read-once retry/invalidation helpers and mutation-plus-verification behavior.

- [ ] **Step 1: Write exact call-translation tests**

For every handler assert arguments, result, and post-action snapshot:

| nib tool | required Cua call |
| --- | --- |
| navigate | URL policy, lazy init if needed, `browser_navigate`, semantic snapshot |
| snapshot | `get_browser_state` with `snapshot_format:"semantic_v2"` |
| click | click-capable raw ref, `browser_click(input_route:"trusted")`, semantic snapshot |
| type | type-capable ref, `browser_type(mode:"insert_text",replace:true)`, semantic snapshot |
| press Enter after type | cached raw editable ref, `browser_type(mode:"keystrokes",text:"\n",replace:false)`, semantic snapshot |
| guarded other key | fresh exact bind proves selected-tab `active:true`, then scoped `press_key`, semantic snapshot |
| scroll | screenshot metrics, `browser_pointer(action:"scroll")`, semantic snapshot |
| vision | `get_browser_state(include_screenshot:true,snapshot_format:"semantic_v2")` and returned PNG content |

Also test blocked URLs never call Cua, stale/unknown aliases fail before Cua, fresh snapshots replace refs, `full=true` consumes continuations only until the character cap, and `full=false` performs one snapshot call.

The type tests must prove the post-action snapshot replaces the now-stale input ref and caches `lastEditable` only when that fresh snapshot has exactly one `type`-capable ref with `states.focused == true`. Any ordinary snapshot/action clears the prior cache; absence or ambiguity leaves it empty and forces the guarded native Enter route.

- [ ] **Step 2: Add failure/retry tests before implementation**

Cover:

```go
func TestCUABrowserReadRebindsAndRetriesOnceOnStaleBinding(t *testing.T)
func TestCUABrowserReadNeverRetriesTwice(t *testing.T)
func TestCUABrowserMutationNeverRetries(t *testing.T)
func TestCUABrowserMutationReportsPostActionSnapshotFailure(t *testing.T)
func TestCUABrowserPreservesStructuredRefusal(t *testing.T)
func TestCUABrowserSeparatesTransportErrorFromRefusal(t *testing.T)
func TestCUABrowserPressRefusesAmbiguousOrInactiveNativeTarget(t *testing.T)
func TestCUABrowserEnterWithoutEditableRefUsesSameGuard(t *testing.T)
```

- [ ] **Step 3: Run and verify failure**

Run: `go test ./mcp -run 'CUABrowser.*(Navigate|Snapshot|Click|Type|Press|Scroll|Vision|Retry|Refusal)' -v`

Expected: FAIL because the handlers are incomplete.

- [ ] **Step 4: Implement common Cua call helpers**

Add:

```go
func (b *cuaBrowserServer) exactArgs(extra map[string]any) (map[string]any, error)
func (b *cuaBrowserServer) snapshot(ctx context.Context, full bool, includeScreenshot bool) (*mcp.CallToolResult, BrowserOutput, error)
func (b *cuaBrowserServer) mutateThenSnapshot(ctx context.Context, tool string, args map[string]any, summary string) (*mcp.CallToolResult, BrowserOutput, error)
func (b *cuaBrowserServer) invalidateOnRefusal(refusal *cuaRefusal)
func isRebindableReadRefusal(code string) bool
```

`exactArgs` injects only private raw `target_id`, selected raw `tab_id`, and runtime `session`. Ref/action validation happens before it returns mutation arguments.

Create one `browserActionTimeout` context for the complete ordinary handler operation, including preflight, action, verification snapshot, continuation, and the single allowed read retry. Preparation gets its separate 60-second context; after initialization, first navigation gets a new ordinary action context. Context cancellation must abort polling and driver calls.

For read retry, only `browser_binding_stale` and `browser_reconnect_exhausted` trigger exact rebinding. Clear stale capability state, bind the same prepared PID/window once, require a sole or uniquely active tab after a generation change, then repeat the read once. Do not use this helper from navigation or any mutation; consent/setup/ref/action refusals remain visible without hidden recovery.

- [ ] **Step 5: Implement snapshot continuation and screenshot forwarding**

For `full=true`, call the first semantic snapshot, then consume each returned continuation exactly once while the rendered text remains below `maxSnapshotChars`. Aggregate outline segments and refs from the same snapshot before installing aliases. Stop before a call whose known output would exceed the limit when possible; otherwise truncate on a line boundary and report incompleteness.

For vision, validate the Cua result's screenshot metadata and require exactly one non-empty `*mcp.ImageContent`. Return a new text label `screenshot for: <question>` plus that image; never forward the raw Cua target/tab fields. Because `get_browser_state` minted a newer snapshot but vision does not expose its refs, clear the prior element/editable aliases instead of retaining stale capabilities or secretly installing unseen ones.

Immediately before guarded native key delivery, perform a fresh bind on the prepared PID/window without the read-retry helper. Require the target to be unchanged and the already selected raw tab to be present with `active:true`; null, false, disappearance, or generation change refuses before delivery. Pass `canonicalKey(in.Key)`, prepared PID/window, and session to `press_key`. This preflight does not make the mutation retryable.

- [ ] **Step 6: Implement scroll coordinates safely**

Because the existing schema has no ref or coordinates, first take an exact-tab screenshot to obtain `viewport_css_width`/`viewport_css_height`. Call `browser_pointer` at the viewport center with `input_route:"trusted"`, `delta_x:0`, and `delta_y` equal to ±90% of viewport height. Fail before mutation if the metrics are absent, non-finite, or non-positive.

- [ ] **Step 7: Run tests and commit**

Run: `gofmt -w mcp/browser_cua.go mcp/browser_cua_results.go mcp/browser_cua_actions_test.go && go test ./mcp -run 'CUABrowser|BrowserPolicy' -v`

Expected: PASS.

```bash
git add mcp/browser_cua.go mcp/browser_cua_results.go mcp/browser_cua_actions_test.go
git commit -m "feat(browser): map nib browser tools onto Cua"
```

---

### Task 9: Add Cua pointer and JavaScript-dialog tools

**Files:**
- Create: `mcp/browser_cua_advanced.go`
- Create: `mcp/browser_cua_advanced_test.go`
- Modify: `mcp/browser_cua.go`

**Interfaces:**
- Produces `browser_pointer` and `browser_dialog` public nib tools with real JSON Schema enums.

- [ ] **Step 1: Write failing schema and action tests**

Assert schemas expose exact enums:

```text
browser_pointer.action: hover right_click double_click scroll drag
browser_pointer.input_route: trusted dom_event
browser_dialog.action: inspect accept dismiss
browser_dialog.delivery_mode: background foreground
```

Test every origin/destination combination, paired coordinates, finite numbers, non-zero scroll delta, drag-only destination fields, and action capability enforcement. A ref-based scroll accepts `scroll` or `pointer`; other ref-based pointer actions require `pointer`.

For dialogs, test inspect returns only `present`, `dialog_id`, and `kind`; accept/dismiss require the current cached `dialog_id`; `prompt_text` is valid only for accepting a prompt; successful resolution clears the dialog and refreshes the snapshot. Inspect may use the one-read rebind retry; accept/dismiss never retry.

- [ ] **Step 2: Run and verify failure**

Run: `go test ./mcp -run 'CUABrowserPointer|CUABrowserDialog' -v`

Expected: FAIL because the tools are absent.

- [ ] **Step 3: Add focused inputs and outputs**

```go
type BrowserPointerInput struct {
	Action         string   `json:"action"`
	InputRoute     string   `json:"input_route,omitempty"`
	Ref            string   `json:"ref,omitempty"`
	X              *float64 `json:"x,omitempty"`
	Y              *float64 `json:"y,omitempty"`
	DestinationRef string   `json:"destination_ref,omitempty"`
	ToX            *float64 `json:"to_x,omitempty"`
	ToY            *float64 `json:"to_y,omitempty"`
	DeltaX         *float64 `json:"delta_x,omitempty"`
	DeltaY         *float64 `json:"delta_y,omitempty"`
}

type BrowserDialogInput struct {
	Action       string `json:"action"`
	DialogID     string `json:"dialog_id,omitempty"`
	PromptText   *string `json:"prompt_text,omitempty"`
	DeliveryMode string `json:"delivery_mode,omitempty"`
}

type BrowserDialogOutput struct {
	BrowserOutcome
	Present      bool   `json:"present,omitempty"`
	DialogID     string `json:"dialog_id,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Action       string `json:"action,omitempty"`
	Snapshot     string `json:"snapshot,omitempty"`
	ElementCount int    `json:"element_count,omitempty"`
}
```

Use pointer fields instead of plain float fields wherever zero is a meaningful supplied coordinate/delta; all coordinate pairs must be both present or both absent. `PromptText` is also a pointer because an explicitly empty prompt response differs from omission.

- [ ] **Step 4: Translate and register**

Translate nib aliases to raw refs only after validation, inject exact target/tab/session fields, and pass no unspecified fields. Default route/delivery in nib to Cua's documented values (`trusted`, `background`) before the call so tests and logs show the selected contract explicitly.

Register both tools only in `startCUABrowserMCPServer`.

- [ ] **Step 5: Run tests and commit**

Run: `gofmt -w mcp/browser_cua_advanced.go mcp/browser_cua_advanced_test.go mcp/browser_cua.go && go test ./mcp -run 'CUABrowserPointer|CUABrowserDialog|BrowserToolContract' -v`

Expected: PASS.

```bash
git add mcp/browser_cua_advanced.go mcp/browser_cua_advanced_test.go mcp/browser_cua.go
git commit -m "feat(browser): expose Cua pointer and dialog actions"
```

---

### Task 10: Confine uploads and downloads to WorkingDir

**Files:**
- Create: `mcp/browser_cua_files.go`
- Create: `mcp/browser_cua_files_test.go`
- Modify: `mcp/browser_cua.go`

**Interfaces:**
- Produces canonical path resolution plus `browser_set_input_files` and `browser_download`.

- [ ] **Step 1: Write failing filesystem-policy tests**

Use a temporary root containing regular files, directories, nested directories, symlinks to in-root and out-of-root targets, missing entries, and traversal paths. Cover:

- empty/absolute/traversing paths rejected;
- every symlink component rejected, even when its final canonical target is inside the root;
- upload must be 1–32 existing regular files;
- download destination defaults to `.`, must exist, and must be a real directory;
- returned canonical path remains under canonical WorkingDir;
- no Cua call is recorded for any validation failure.

- [ ] **Step 2: Run and verify failure**

Run: `go test ./mcp -run 'BrowserPath|CUABrowserFiles|CUABrowserDownload' -v`

Expected: FAIL because the helpers and tools do not exist.

- [ ] **Step 3: Implement component-safe resolution**

Use one helper with a required kind:

```go
type browserPathKind int
const (
	browserUploadFile browserPathKind = iota
	browserDownloadDir
)

func resolveBrowserPath(root, relative string, kind browserPathKind) (string, error)
```

Algorithm:

1. reject empty upload input, every absolute/volume-qualified public input, and any path containing a literal `..` component before cleaning;
2. resolve an empty WorkingDir with `os.Getwd`, then canonicalize the existing root with `filepath.EvalSymlinks`;
3. clean/join the relative path and use `filepath.Rel` as a second containment check;
4. walk each path component from the canonical root with `os.Lstat` and reject symlinks;
5. require an existing regular file or directory according to kind;
6. `EvalSymlinks` the final path and repeat the `filepath.Rel` containment check; and
7. return the canonical absolute path only to the private Cua call builder.

- [ ] **Step 4: Add focused tool contracts**

```go
type BrowserSetInputFilesInput struct {
	Ref   string   `json:"ref"`
	Files []string `json:"files"`
}

type BrowserSetInputFilesOutput struct {
	BrowserOutcome
	AssignedCount int    `json:"assigned_count,omitempty"`
	Snapshot      string `json:"snapshot,omitempty"`
	ElementCount  int    `json:"element_count,omitempty"`
}

type BrowserDownloadInput struct {
	Ref       string `json:"ref"`
	Directory string `json:"directory,omitempty"`
}

type BrowserDownloadOutput struct {
	BrowserOutcome
	DownloadID  string `json:"download_id,omitempty"`
	Bytes       int64  `json:"bytes,omitempty"`
	Snapshot    string `json:"snapshot,omitempty"`
	ElementCount int   `json:"element_count,omitempty"`
}
```

Uploads require the current ref's `upload` action; downloads require `click`, which is the Cua 0.19 capability required by `browser_download`. Translate only after path validation. Pass canonical absolute paths as `files` or `destination_root`; do not inject Cua's private approval marker. The Cua MCP proxy owns its destructive-tool proof.

Decode Cua's upload result field `file_count` and expose it as nib's `assigned_count`; do not assume the public and driver field names match.

Return only count/opaque download ID/byte count plus the fresh snapshot. Never return the resolved directory, source URL, filename, or raw ref.

- [ ] **Step 5: Prove non-retry and cleanup behavior**

Add tests where the Cua mutation succeeds but the verification snapshot fails, and where Cua returns a refusal. Assert exactly one upload/download call and no leaked path in output/error text.

- [ ] **Step 6: Run tests and commit**

Run: `gofmt -w mcp/browser_cua_files.go mcp/browser_cua_files_test.go mcp/browser_cua.go && go test ./mcp -run 'BrowserPath|CUABrowserFiles|CUABrowserDownload' -v`

Expected: PASS.

```bash
git add mcp/browser_cua_files.go mcp/browser_cua_files_test.go mcp/browser_cua.go
git commit -m "feat(browser): add confined Cua file transfer tools"
```

---

### Task 11: Add opt-in live Cua integration coverage

**Files:**
- Create: `mcp/browser_cua_integration_test.go`
- Modify: `mcp/browser_integration_test.go` only if a fixture helper can be shared without changing the `browserintegration` contract

**Interfaces:**
- Adds build tag `cuabrowserintegration`; normal tests remain fully hermetic.

- [ ] **Step 1: Build a local multi-feature fixture**

Behind `//go:build cuabrowserintegration`, serve local HTML with:

- a form/input and submitted-state marker;
- hover/double-click/scroll/drag observable state;
- a link that opens a second tab;
- a file input whose selected count is visible;
- alert/confirm/prompt triggers; and
- a same-origin attachment response for download.

Set `WorkingDir=t.TempDir()`, `AllowPrivateURLs=true`, a unique `ProfileName` and `SessionID`, and create only test-owned upload/download files beneath that directory.

- [ ] **Step 2: Write the live flow**

Exercise the actual runtime and adapter, not raw Cua tools:

```go
func TestCUABrowserIntegrationPrepareNavigateAndSnapshot(t *testing.T)
func TestCUABrowserIntegrationTypeAndEnter(t *testing.T)
func TestCUABrowserIntegrationPointerAndTabs(t *testing.T)
func TestCUABrowserIntegrationFileDialogAndDownload(t *testing.T)
func TestCUABrowserIntegrationVisionUsesExactTab(t *testing.T)
```

End the runtime in `t.Cleanup`; use no personal profile and no external network. Assert observable page state after every mutation.

- [ ] **Step 3: Compile normal and tagged test suites**

Run: `go test ./mcp -run '^$'`

Expected: PASS; ordinary compilation does not pull in the live test.

Run on a suitable GUI host with Cua Driver 0.19.0+ installed:

```bash
go test -tags cuabrowserintegration ./mcp -run CUABrowserIntegration -v
```

Expected: PASS. If the current host lacks a display/Cua/Chromium, record `NOT RUN` with the missing prerequisite; do not weaken the tests into silent skips after the tag was explicitly requested.

- [ ] **Step 4: Commit**

```bash
git add mcp/browser_cua_integration_test.go mcp/browser_integration_test.go
git commit -m "test(browser): cover the live Cua browser route"
```

Omit `mcp/browser_integration_test.go` from `git add` if it was not changed.

---

### Task 12: Document rollout, migration, and verification

**Files:**
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-08-07-cua-browser-backend-design.md` only if implementation discoveries require an approved clarification

**Interfaces:**
- Documents selection without changing the default or implying Chromedp removal.

- [ ] **Step 1: Add configuration and migration documentation**

Under `README.md` Configuration, add a compact section covering:

```yaml
cua:
  command: cua-driver
  args: [mcp]
  env: {}
  session_id: ""

browser:
  enabled: true
  backend: cua
  chrome_path: ""
  profile_name: nib
  allow_private_urls: false
```

State:

- empty/`chromedp` remains the default;
- Cua requires 0.19.0+ and the complete browser tool surface;
- no automatic fallback occurs;
- `profile_dir` remains Chromedp-only and existing personal profiles are not attached or migrated;
- top-level `cua` replaces deprecated programmatic `Computer.Command/Args/Env/SessionID` fields;
- one runtime/session is shared when both browser and computer are enabled;
- uploads/downloads accept only relative WorkingDir paths;
- list all seven existing and six new tools; and
- show the opt-in live-test command and prerequisites.

- [ ] **Step 2: Run formatting, static checks, and all hermetic tests**

Run `gofmt` only on files changed by the implementation (obtain the list from `git diff --name-only ba60694 -- '*.go'` and pass those explicit paths), then run:

```bash
go vet ./...
go build ./...
go test -p 1 ./...
```

Expected: all commands PASS. Do not bulk-format untouched package files; several may contain unrelated user or baseline formatting.

- [ ] **Step 3: Review the final diff against the design**

Run:

```bash
git diff --check
git status --short
git diff --stat ba60694..HEAD
git diff --stat
```

Manually verify every success criterion and every hermetic test bullet in the approved design. Confirm the user's pre-existing untracked artifacts remain unmodified and unstaged.

- [ ] **Step 4: Commit documentation**

```bash
git add README.md
git commit -m "docs(browser): explain the opt-in Cua backend"
```

- [ ] **Step 5: Request code review before integration**

Use `superpowers:requesting-code-review` with the approved design, this plan, the full commit range after `ba60694`, hermetic gate output, and live-suite result (`PASS` or explicit `NOT RUN: <prerequisite>`). Resolve findings through `superpowers:receiving-code-review`, rerun the completion gate, then use `superpowers:finishing-a-development-branch` for the integration choice.
