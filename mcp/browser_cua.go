package mcp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/types"
	"github.com/mudler/xlog"
)

const cuaBrowserPrepareTimeout = 60 * time.Second

const cuaBrowserWindowPollInterval = 100 * time.Millisecond

const maxCUASnapshotContinuations = 64

type cuaBrowserServer struct {
	runtime *cuaRuntime
	cfg     types.Config

	actionMu sync.Mutex
	mu       sync.Mutex

	preparedPID    int
	windowID       int
	targetID       string
	tabs           map[string]cuaTab
	tabAliases     map[string]string
	tabReverse     map[string]string
	nextTabAlias   int
	selectedTab    string
	refs           map[string]cuaElement
	lastEditable   string
	dialogID       string
	dialogKind     string
	cleanupWarning string

	prepareTimeout time.Duration
	pollInterval   time.Duration
}

type cuaBrowserApp struct {
	Name           string `json:"name"`
	BundleID       string `json:"bundle_id"`
	Path           string `json:"path"`
	LaunchPath     string `json:"launch_path"`
	ExecutablePath string `json:"executable_path"`
}

type cuaAppListResult struct {
	Status string          `json:"status"`
	Apps   []cuaBrowserApp `json:"apps"`
}

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

func newCUABrowserServer(runtime *cuaRuntime, cfg types.Config) *cuaBrowserServer {
	aliases := newCUAAliasState()
	return &cuaBrowserServer{
		runtime:        runtime,
		cfg:            cfg,
		tabs:           aliases.tabs,
		tabAliases:     aliases.tabAliases,
		tabReverse:     aliases.tabReverse,
		refs:           aliases.refs,
		prepareTimeout: cuaBrowserPrepareTimeout,
		pollInterval:   cuaBrowserWindowPollInterval,
	}
}

func (b *cuaBrowserServer) browserNavigate(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in BrowserInput,
) (*mcp.CallToolResult, BrowserOutput, error) {
	url := strings.TrimSpace(in.URL)
	if url == "" {
		return nil, BrowserOutput{}, fmt.Errorf("browser_navigate needs a url")
	}
	if err := checkURLAllowed(url, b.cfg.Browser.AllowPrivateURLs); err != nil {
		return nil, BrowserOutput{}, err
	}

	b.actionMu.Lock()
	defer b.actionMu.Unlock()

	if !b.isPrepared() {
		refusal, err := b.prepare(ctx)
		if err != nil {
			return nil, BrowserOutput{}, err
		}
		if refusal != nil {
			return b.browserRefusalResult(refusal, nil)
		}
	}

	actionCtx, cancel := context.WithTimeout(ctx, browserActionTimeout)
	defer cancel()
	args, err := b.exactArgs(map[string]any{"url": url, "snapshot_format": "semantic_v2"})
	if err != nil {
		return nil, BrowserOutput{}, err
	}
	var envelope cuaResultEnvelope
	refusal, err := b.call(actionCtx, "browser_navigate", args, &envelope)
	if err != nil {
		b.clearTabScopedCapabilities()
		return nil, BrowserOutput{}, b.publicCallError(err, args)
	}
	if refusal != nil {
		b.invalidateOnRefusal(refusal)
		return b.browserRefusalResult(refusal, args)
	}
	// A successful navigation invalidates every capability from the prior
	// document before its returned verification snapshot is trusted.
	b.clearTabScopedCapabilities()
	if err := b.validateSelectedSnapshot(envelope); err != nil {
		return nil, BrowserOutput{}, err
	}

	b.mu.Lock()
	state := b.aliasStateLocked()
	output := renderCUASnapshot(state, envelope)
	b.installAliasStateLocked(state)
	warning := b.cleanupWarning
	if warning != "" {
		b.cleanupWarning = ""
		if output.Snapshot != "" && !strings.HasSuffix(output.Snapshot, "\n") {
			output.Snapshot += "\n"
		}
		output.Snapshot += warning + "\n"
	}
	b.mu.Unlock()
	return textResult(output.Snapshot), output, nil
}

func (b *cuaBrowserServer) prepare(ctx context.Context) (retRefusal *cuaRefusal, retErr error) {
	if b.runtime == nil {
		return nil, errors.New("Cua browser backend requires a shared runtime")
	}
	prepareCtx, cancel := context.WithTimeout(ctx, b.prepareTimeout)
	defer cancel()

	var appResult cuaAppListResult
	refusal, err := b.call(prepareCtx, "list_apps", b.sessionArgs(nil), &appResult)
	if err != nil || refusal != nil {
		return refusal, err
	}
	var windowResult cuaResultEnvelope
	refusal, err = b.call(prepareCtx, "list_windows", b.sessionArgs(nil), &windowResult)
	if err != nil || refusal != nil {
		return refusal, err
	}

	apps := b.supportedApps(appResult.Apps)
	if len(apps) == 0 {
		if b.cfg.Browser.ChromePath != "" {
			return nil, fmt.Errorf("no Chrome, Chromium, or Edge app matches browser.chrome_path")
		}
		return nil, fmt.Errorf("no Chrome, Chromium, or Edge app is available for Cua preparation")
	}

	sourcePID, sourceWindow := pickRunningBrowser(
		apps,
		windowResult.Windows,
		normalizedExecutableIdentity(b.cfg.Browser.ChromePath),
	)
	ownedPID := 0
	cleanupAttempted := false
	defer func() {
		if ownedPID == 0 || cleanupAttempted {
			return
		}
		if cleanupErr := b.killOwnedSource(context.Background(), ownedPID); cleanupErr != nil {
			if retRefusal != nil {
				refusalCopy := *retRefusal
				refusalCopy.Message += "; temporary source browser cleanup failed"
				retRefusal = &refusalCopy
				return
			}
			retErr = errors.Join(retErr, cleanupErr)
		}
	}()

	if sourcePID == 0 {
		launchArgs, launchErr := b.launchArgs(apps[0])
		if launchErr != nil {
			return nil, launchErr
		}
		var launchResult cuaResultEnvelope
		refusal, err = b.call(prepareCtx, "launch_app", b.sessionArgs(launchArgs), &launchResult)
		if err != nil || refusal != nil {
			return refusal, err
		}
		if launchResult.PID <= 0 {
			return nil, fmt.Errorf("Cua launch_app returned no positive source pid")
		}
		ownedPID = launchResult.PID
		sourcePID = launchResult.PID
		sourceWindow, refusal, err = b.waitForExactWindow(prepareCtx, sourcePID)
		if err != nil || refusal != nil {
			return refusal, err
		}
	}
	_ = sourceWindow // Its existence is the source identity proof required by prepare.

	profileName, err := cuaProfileName(b.cfg.Browser)
	if err != nil {
		return nil, err
	}
	var prepareResult cuaResultEnvelope
	refusal, err = b.call(prepareCtx, "browser_prepare", b.sessionArgs(map[string]any{
		"pid":          sourcePID,
		"allow_launch": true,
		"profile":      map[string]any{"mode": "isolated_named", "name": profileName},
	}), &prepareResult)
	if err != nil || refusal != nil {
		return refusal, err
	}
	if prepareResult.PreparedPID <= 0 {
		return nil, fmt.Errorf("Cua browser_prepare returned no positive prepared_pid")
	}
	// Once preparation adopts our temporary source process, ownership transfers
	// to the declared Cua session immediately. Any later window, bind, or tab
	// failure must leave that prepared process for end_session to clean up.
	if ownedPID != 0 && ownedPID == prepareResult.PreparedPID {
		ownedPID = 0
	}
	preparedWindow, refusal, err := b.waitForExactWindow(prepareCtx, prepareResult.PreparedPID)
	if err != nil || refusal != nil {
		return refusal, err
	}

	var bind cuaResultEnvelope
	refusal, err = b.call(prepareCtx, "get_browser_state", b.sessionArgs(map[string]any{
		"pid": prepareResult.PreparedPID, "window_id": preparedWindow,
	}), &bind)
	if err != nil || refusal != nil {
		return refusal, err
	}
	if err := validateExactBinding(bind); err != nil {
		return nil, err
	}
	selected, selectionRefusal := selectUnambiguousTab(bind.Tabs)
	if selectionRefusal != nil {
		return selectionRefusal, nil
	}

	b.mu.Lock()
	state := b.aliasStateLocked()
	state.observeTarget(bind.TargetID)
	state.syncTabs(bind.Tabs)
	state.selectedTab = selected
	b.preparedPID = prepareResult.PreparedPID
	b.windowID = preparedWindow
	b.installAliasStateLocked(state)
	b.mu.Unlock()

	if ownedPID != 0 && ownedPID != prepareResult.PreparedPID {
		cleanupAttempted = true
		if cleanupErr := b.killOwnedSource(prepareCtx, ownedPID); cleanupErr != nil {
			b.mu.Lock()
			b.cleanupWarning = "warning: temporary source browser cleanup failed; the prepared browser remains usable"
			b.mu.Unlock()
		}
	} else if ownedPID != 0 {
		cleanupAttempted = true
	}
	return nil, nil
}

func (b *cuaBrowserServer) browserTabs(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	_ BrowserTabsInput,
) (*mcp.CallToolResult, BrowserTabsOutput, error) {
	b.actionMu.Lock()
	defer b.actionMu.Unlock()
	if err := b.requirePrepared(); err != nil {
		return nil, BrowserTabsOutput{}, err
	}

	actionCtx, cancel := context.WithTimeout(ctx, browserActionTimeout)
	defer cancel()
	args, err := b.bindingArgs()
	if err != nil {
		return nil, BrowserTabsOutput{}, err
	}
	var bind cuaResultEnvelope
	refusal, err := b.call(actionCtx, "get_browser_state", args, &bind)
	if err != nil {
		return nil, BrowserTabsOutput{}, err
	}
	if refusal != nil {
		b.invalidateOnRefusal(refusal)
		return b.tabsRefusalResult(refusal, args)
	}
	b.mu.Lock()
	previousTarget := b.targetID
	b.mu.Unlock()
	targetChanged := bind.TargetID != "" && bind.TargetID != previousTarget
	if targetChanged {
		b.observeTargetGeneration(bind.TargetID)
	}
	if err := validateExactBinding(bind); err != nil {
		return nil, BrowserTabsOutput{}, err
	}

	b.mu.Lock()
	state := b.aliasStateLocked()
	previousSelected := state.selectedTab
	state.observeTarget(bind.TargetID)
	state.syncTabs(bind.Tabs)
	selectionChanged := state.selectedTab != previousSelected
	if targetChanged || state.selectedTab == "" || state.tabs[state.selectedTab].ID == "" {
		selected, selectionRefusal := selectUnambiguousTab(bind.Tabs)
		if selectionRefusal != nil {
			if selectionChanged {
				clearCUATabScopedCapabilities(state)
			}
			b.installAliasStateLocked(state)
			b.mu.Unlock()
			return b.tabsRefusalResult(selectionRefusal, nil)
		}
		state.selectedTab = selected
		selectionChanged = selected != previousSelected
	}
	if selectionChanged {
		clearCUATabScopedCapabilities(state)
	}
	b.installAliasStateLocked(state)
	output := b.tabsOutputLocked(bind.Tabs)
	b.mu.Unlock()
	return textResult(renderBrowserTabs(output.Tabs)), output, nil
}

func (b *cuaBrowserServer) browserSelectTab(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in BrowserSelectTabInput,
) (*mcp.CallToolResult, BrowserOutput, error) {
	b.actionMu.Lock()
	defer b.actionMu.Unlock()
	if err := b.requirePrepared(); err != nil {
		return nil, BrowserOutput{}, err
	}

	b.mu.Lock()
	rawTab := b.tabAliases[strings.TrimSpace(in.TabID)]
	if rawTab == "" {
		b.mu.Unlock()
		return nil, BrowserOutput{}, fmt.Errorf("unknown or stale tab alias %q; call browser_tabs", in.TabID)
	}
	targetID := b.targetID
	b.mu.Unlock()

	actionCtx, cancel := context.WithTimeout(ctx, browserActionTimeout)
	defer cancel()
	args := b.sessionArgs(map[string]any{
		"target_id": targetID, "tab_id": rawTab, "snapshot_format": "semantic_v2",
	})
	var snapshot cuaResultEnvelope
	refusal, err := b.call(actionCtx, "get_browser_state", args, &snapshot)
	if err != nil {
		return nil, BrowserOutput{}, err
	}
	if refusal != nil {
		b.invalidateOnRefusal(refusal)
		return b.browserRefusalResult(refusal, args)
	}
	if snapshot.TargetID == "" {
		return nil, BrowserOutput{}, errors.New("Cua browser_select_tab snapshot omitted target identity")
	}
	if snapshot.TargetID != targetID {
		b.mu.Lock()
		state := b.aliasStateLocked()
		state.observeTarget(snapshot.TargetID)
		b.installAliasStateLocked(state)
		b.mu.Unlock()
		return b.browserRefusalResult(&cuaRefusal{
			Code: "browser_target_changed", Message: "browser target changed; call browser_tabs and select a current tab",
		}, nil)
	}
	if snapshot.TabID == "" {
		return nil, BrowserOutput{}, errors.New("Cua browser_select_tab snapshot omitted tab identity")
	}
	if snapshot.TabID != rawTab {
		return nil, BrowserOutput{}, errors.New("Cua browser_select_tab returned a snapshot for a different tab")
	}
	if snapshot.Snapshot.Format != "semantic_v2" {
		return nil, BrowserOutput{}, fmt.Errorf("Cua snapshot format is %q, want semantic_v2", snapshot.Snapshot.Format)
	}
	if snapshot.Snapshot.ID == "" {
		return nil, BrowserOutput{}, errors.New("Cua semantic snapshot omitted snapshot id")
	}

	b.mu.Lock()
	state := b.aliasStateLocked()
	output := renderCUASnapshot(state, snapshot)
	state.selectedTab = rawTab
	b.installAliasStateLocked(state)
	b.mu.Unlock()
	return textResult(output.Snapshot), output, nil
}

func (b *cuaBrowserServer) browserSnapshot(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in BrowserInput,
) (*mcp.CallToolResult, BrowserOutput, error) {
	b.actionMu.Lock()
	defer b.actionMu.Unlock()
	if err := b.requirePrepared(); err != nil {
		return nil, BrowserOutput{}, err
	}
	actionCtx, cancel := context.WithTimeout(ctx, browserActionTimeout)
	defer cancel()
	return b.snapshot(actionCtx, in.Full, false)
}

func (b *cuaBrowserServer) browserClick(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in BrowserInput,
) (*mcp.CallToolResult, BrowserOutput, error) {
	b.actionMu.Lock()
	defer b.actionMu.Unlock()
	if err := b.requirePrepared(); err != nil {
		return nil, BrowserOutput{}, err
	}
	element, err := b.actionableRef(in.Ref, "click")
	if err != nil {
		return nil, BrowserOutput{}, err
	}
	actionCtx, cancel := context.WithTimeout(ctx, browserActionTimeout)
	defer cancel()
	args, err := b.exactArgs(map[string]any{"ref": element.Raw, "input_route": "trusted"})
	if err != nil {
		return nil, BrowserOutput{}, err
	}
	return b.mutateThenSnapshot(actionCtx, "browser_click", args, "click")
}

func (b *cuaBrowserServer) browserType(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in BrowserInput,
) (*mcp.CallToolResult, BrowserOutput, error) {
	if in.Text == "" {
		return nil, BrowserOutput{}, fmt.Errorf("browser_type needs text")
	}
	b.actionMu.Lock()
	defer b.actionMu.Unlock()
	if err := b.requirePrepared(); err != nil {
		return nil, BrowserOutput{}, err
	}
	element, err := b.actionableRef(in.Ref, "type")
	if err != nil {
		return nil, BrowserOutput{}, err
	}
	actionCtx, cancel := context.WithTimeout(ctx, browserActionTimeout)
	defer cancel()
	args, err := b.exactArgs(map[string]any{
		"ref": element.Raw, "text": in.Text, "mode": "insert_text", "replace": true,
	})
	if err != nil {
		return nil, BrowserOutput{}, err
	}
	result, output, snapshotResult, err := b.mutateThenSnapshotEnvelope(actionCtx, "browser_type", args, "type")
	if err != nil || output.Status != "ok" {
		return result, output, err
	}
	raw := uniqueFocusedEditable(snapshotResult.Refs)
	b.mu.Lock()
	if raw != "" && b.containsRawRefLocked(raw) {
		b.lastEditable = raw
	}
	b.mu.Unlock()
	return result, output, nil
}

func (b *cuaBrowserServer) browserPress(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in BrowserInput,
) (*mcp.CallToolResult, BrowserOutput, error) {
	if _, err := keyForName(in.Key); err != nil {
		return nil, BrowserOutput{}, err
	}
	b.actionMu.Lock()
	defer b.actionMu.Unlock()
	if err := b.requirePrepared(); err != nil {
		return nil, BrowserOutput{}, err
	}
	actionCtx, cancel := context.WithTimeout(ctx, browserActionTimeout)
	defer cancel()

	b.mu.Lock()
	editable := b.lastEditable
	b.mu.Unlock()
	if in.Key == "Enter" && editable != "" {
		args, err := b.exactArgs(map[string]any{
			"ref": editable, "text": "\n", "mode": "keystrokes", "replace": false,
		})
		if err != nil {
			return nil, BrowserOutput{}, err
		}
		return b.mutateThenSnapshot(actionCtx, "browser_type", args, "press Enter")
	}

	refusal, err := b.guardNativeKeyTarget(actionCtx)
	if err != nil {
		return nil, BrowserOutput{}, err
	}
	if refusal != nil {
		return b.browserRefusalResult(refusal, nil)
	}
	b.mu.Lock()
	pid, windowID := b.preparedPID, b.windowID
	b.mu.Unlock()
	args := b.sessionArgs(map[string]any{
		"pid": pid, "window_id": windowID, "key": canonicalKey(in.Key),
	})
	return b.mutateThenSnapshot(actionCtx, "press_key", args, "press "+in.Key)
}

func (b *cuaBrowserServer) browserScroll(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in BrowserInput,
) (*mcp.CallToolResult, BrowserOutput, error) {
	if in.Direction != "up" && in.Direction != "down" {
		return nil, BrowserOutput{}, fmt.Errorf("browser_scroll: direction must be up or down")
	}
	b.actionMu.Lock()
	defer b.actionMu.Unlock()
	if err := b.requirePrepared(); err != nil {
		return nil, BrowserOutput{}, err
	}
	actionCtx, cancel := context.WithTimeout(ctx, browserActionTimeout)
	defer cancel()
	_, output, screenshotState, err := b.snapshotInternal(actionCtx, false, true, false)
	if err != nil || output.Status != "ok" {
		return nil, output, err
	}
	b.clearTabScopedCapabilities()
	metrics := screenshotState.Screenshot
	if err := validateScreenshotMetrics(metrics); err != nil {
		return nil, BrowserOutput{}, err
	}
	deltaY := metrics.ViewportCSSHeight * 0.9
	if in.Direction == "up" {
		deltaY = -deltaY
	}
	args, err := b.exactArgs(map[string]any{
		"action": "scroll", "input_route": "trusted",
		"x": metrics.ViewportCSSWidth / 2, "y": metrics.ViewportCSSHeight / 2,
		"delta_x": float64(0), "delta_y": deltaY,
	})
	if err != nil {
		return nil, BrowserOutput{}, err
	}
	return b.mutateThenSnapshot(actionCtx, "browser_pointer", args, "scroll "+in.Direction)
}

func (b *cuaBrowserServer) browserVision(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in BrowserInput,
) (*mcp.CallToolResult, BrowserOutput, error) {
	b.actionMu.Lock()
	defer b.actionMu.Unlock()
	if err := b.requirePrepared(); err != nil {
		return nil, BrowserOutput{}, err
	}
	actionCtx, cancel := context.WithTimeout(ctx, browserActionTimeout)
	defer cancel()
	result, output, state, err := b.snapshotInternal(actionCtx, false, true, true)
	if err != nil || output.Status != "ok" {
		return result, output, err
	}
	b.clearTabScopedCapabilities()
	if err := validateScreenshotMetadata(state.Screenshot); err != nil {
		return nil, BrowserOutput{}, err
	}
	var image *mcp.ImageContent
	for _, content := range result.Content {
		candidate, ok := content.(*mcp.ImageContent)
		if !ok {
			continue
		}
		if image != nil {
			return nil, BrowserOutput{}, errors.New("Cua browser_vision returned multiple images")
		}
		image = candidate
	}
	if image == nil || len(image.Data) == 0 {
		return nil, BrowserOutput{}, errors.New("Cua browser_vision returned no non-empty image")
	}
	if image.MIMEType != "image/png" {
		return nil, BrowserOutput{}, fmt.Errorf("Cua browser_vision returned image MIME type %q, want image/png", image.MIMEType)
	}
	imageCopy := &mcp.ImageContent{MIMEType: "image/png", Data: append([]byte(nil), image.Data...)}
	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: "screenshot for: " + in.Question}, imageCopy,
	}}, BrowserOutput{}, nil
}

func (b *cuaBrowserServer) call(
	ctx context.Context,
	name string,
	args map[string]any,
	dst any,
) (*cuaRefusal, error) {
	_, refusal, err := b.callResult(ctx, name, args, dst)
	return refusal, err
}

func (b *cuaBrowserServer) callResult(
	ctx context.Context,
	name string,
	args map[string]any,
	dst any,
) (*mcp.CallToolResult, *cuaRefusal, error) {
	if b.runtime == nil {
		return nil, nil, errors.New("Cua browser backend requires a shared runtime")
	}
	result, err := b.runtime.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, nil, fmt.Errorf("Cua %s: %w", name, err)
	}
	refusal, err := decodeCUAResult(result, dst)
	if err != nil {
		return nil, nil, fmt.Errorf("Cua %s result: %w", name, err)
	}
	return result, refusal, nil
}

func (b *cuaBrowserServer) sessionArgs(extra map[string]any) map[string]any {
	args := make(map[string]any, len(extra)+1)
	for key, value := range extra {
		args[key] = value
	}
	args["session"] = b.runtime.SessionID()
	return args
}

func (b *cuaBrowserServer) selectedExactArgs(extra map[string]any) (map[string]any, error) {
	return b.exactArgs(extra)
}

func (b *cuaBrowserServer) exactArgs(extra map[string]any) (map[string]any, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.preparedPID <= 0 || b.windowID <= 0 || b.targetID == "" || b.selectedTab == "" {
		return nil, fmt.Errorf("no page open — call browser_navigate first")
	}
	args := make(map[string]any, len(extra)+2)
	for key, value := range extra {
		args[key] = value
	}
	args["target_id"] = b.targetID
	args["tab_id"] = b.selectedTab
	return b.sessionArgs(args), nil
}

func (b *cuaBrowserServer) actionableRef(alias, action string) (cuaElement, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	element, ok := b.refs[strings.TrimSpace(alias)]
	if !ok || element.Raw == "" {
		return cuaElement{}, fmt.Errorf("unknown or stale element ref %q; call browser_snapshot", alias)
	}
	if !element.Actions[action] {
		return cuaElement{}, fmt.Errorf("element ref %q does not support %s; call browser_snapshot", alias, action)
	}
	return element, nil
}

func (b *cuaBrowserServer) containsRawRefLocked(raw string) bool {
	for _, element := range b.refs {
		if element.Raw == raw {
			return true
		}
	}
	return false
}

func (b *cuaBrowserServer) clearTabScopedCapabilities() {
	b.mu.Lock()
	state := b.aliasStateLocked()
	clearCUATabScopedCapabilities(state)
	b.installAliasStateLocked(state)
	b.mu.Unlock()
}

func uniqueFocusedEditable(refs []cuaSemanticRef) string {
	focused := ""
	for _, ref := range refs {
		if ref.Ref == "" || !semanticRefSupports(ref, "type") || !semanticStateTrue(ref.States, "focused") {
			continue
		}
		if focused != "" {
			return ""
		}
		focused = ref.Ref
	}
	return focused
}

func semanticRefSupports(ref cuaSemanticRef, action string) bool {
	for _, candidate := range ref.Actions {
		if candidate == action {
			return true
		}
	}
	return false
}

func semanticStateTrue(states map[string]any, name string) bool {
	value, ok := states[name].(bool)
	return ok && value
}

func (b *cuaBrowserServer) snapshot(
	ctx context.Context,
	full bool,
	includeScreenshot bool,
) (*mcp.CallToolResult, BrowserOutput, error) {
	result, output, _, err := b.snapshotInternal(ctx, full, includeScreenshot, true)
	if err == nil && output.Status == "ok" {
		result = textResult(output.Snapshot)
	}
	return result, output, err
}

func (b *cuaBrowserServer) snapshotInternal(
	ctx context.Context,
	full bool,
	includeScreenshot bool,
	allowRetry bool,
) (*mcp.CallToolResult, BrowserOutput, cuaResultEnvelope, error) {
	args, err := b.exactArgs(map[string]any{"snapshot_format": "semantic_v2"})
	if err != nil {
		return nil, BrowserOutput{}, cuaResultEnvelope{}, err
	}
	if includeScreenshot {
		args["include_screenshot"] = true
	}
	retryAvailable := allowRetry
	result, envelope, refusal, err := b.readSnapshotPage(ctx, args, &retryAvailable, "")
	if err != nil {
		return nil, BrowserOutput{}, cuaResultEnvelope{}, err
	}
	if refusal != nil {
		refusalResult, output, refusalErr := b.browserRefusalResult(refusal, args)
		return refusalResult, output, cuaResultEnvelope{}, refusalErr
	}
	// get_browser_state minted a new snapshot even if later identity or
	// continuation validation fails. Older element/editable capabilities can
	// no longer be used safely.
	b.clearTabScopedCapabilities()
	if err := b.validateSelectedSnapshot(envelope); err != nil {
		return nil, BrowserOutput{}, cuaResultEnvelope{}, err
	}

	aggregate := envelope
	seenContinuations := make(map[string]struct{})
	for page := 0; full && aggregate.Snapshot.Continuation != "" && page < maxCUASnapshotContinuations; page++ {
		if rendered := renderCUASnapshotCopy(b, aggregate); len(rendered.Snapshot) >= maxSnapshotChars ||
			strings.Contains(rendered.Snapshot, "snapshot truncated at nib limit") {
			break
		}
		token := aggregate.Snapshot.Continuation
		if _, seen := seenContinuations[token]; seen {
			break
		}
		seenContinuations[token] = struct{}{}
		continuationArgs, exactErr := b.exactArgs(map[string]any{
			"snapshot_format": "semantic_v2", "continuation": token,
		})
		if exactErr != nil {
			return nil, BrowserOutput{}, cuaResultEnvelope{}, exactErr
		}
		_, next, continuationRefusal, continuationErr := b.readSnapshotPage(
			ctx, continuationArgs, &retryAvailable, aggregate.TargetID,
		)
		if continuationErr != nil {
			return nil, BrowserOutput{}, cuaResultEnvelope{}, continuationErr
		}
		if continuationRefusal != nil {
			refusalResult, output, refusalErr := b.browserRefusalResult(continuationRefusal, continuationArgs)
			return refusalResult, output, cuaResultEnvelope{}, refusalErr
		}
		if err := validateSnapshotContinuation(aggregate, next); err != nil {
			if next.TargetID != "" && next.TargetID != aggregate.TargetID {
				b.observeTargetGeneration(next.TargetID)
			}
			return nil, BrowserOutput{}, cuaResultEnvelope{}, err
		}
		if err := b.validateSelectedSnapshot(next); err != nil {
			return nil, BrowserOutput{}, cuaResultEnvelope{}, err
		}
		aggregate = appendSnapshotPage(aggregate, next)
	}

	b.mu.Lock()
	state := b.aliasStateLocked()
	output := renderCUASnapshot(state, aggregate)
	b.installAliasStateLocked(state)
	b.mu.Unlock()
	return result, output, aggregate, nil
}

func renderCUASnapshotCopy(b *cuaBrowserServer, envelope cuaResultEnvelope) BrowserOutput {
	b.mu.Lock()
	state := b.aliasStateLocked()
	b.mu.Unlock()
	state.tabs = cloneCUATabs(state.tabs)
	state.tabAliases = cloneStringMap(state.tabAliases)
	state.tabReverse = cloneStringMap(state.tabReverse)
	state.refs = cloneCUARefs(state.refs)
	return renderCUASnapshot(state, envelope)
}

func cloneCUATabs(source map[string]cuaTab) map[string]cuaTab {
	cloned := make(map[string]cuaTab, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneStringMap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneCUARefs(source map[string]cuaElement) map[string]cuaElement {
	cloned := make(map[string]cuaElement, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func (b *cuaBrowserServer) readSnapshotPage(
	ctx context.Context,
	args map[string]any,
	retryAvailable *bool,
	pinnedTarget string,
) (*mcp.CallToolResult, cuaResultEnvelope, *cuaRefusal, error) {
	var envelope cuaResultEnvelope
	result, refusal, err := b.callResult(ctx, "get_browser_state", args, &envelope)
	if err != nil || refusal == nil || retryAvailable == nil || !*retryAvailable || !isRebindableReadRefusal(refusal.Code) {
		if refusal != nil {
			b.invalidateOnRefusal(refusal)
		}
		return result, envelope, refusal, err
	}
	*retryAvailable = false
	b.mu.Lock()
	oldTarget, oldSelected := b.targetID, b.selectedTab
	b.mu.Unlock()
	b.invalidateOnRefusal(refusal)
	if rebindRefusal, rebindErr := b.rebindExact(ctx, oldTarget, oldSelected); rebindErr != nil {
		return nil, cuaResultEnvelope{}, nil, rebindErr
	} else if rebindRefusal != nil {
		return nil, cuaResultEnvelope{}, rebindRefusal, nil
	}
	if pinnedTarget != "" {
		b.mu.Lock()
		reboundTarget := b.targetID
		b.mu.Unlock()
		if reboundTarget != pinnedTarget {
			return nil, cuaResultEnvelope{}, nil, errors.New(
				"Cua snapshot generation changed during continuation; retry browser_snapshot",
			)
		}
	}
	retryArgs := make(map[string]any, len(args))
	for key, value := range args {
		retryArgs[key] = value
	}
	b.mu.Lock()
	retryArgs["target_id"] = b.targetID
	retryArgs["tab_id"] = b.selectedTab
	b.mu.Unlock()
	envelope = cuaResultEnvelope{}
	result, refusal, err = b.callResult(ctx, "get_browser_state", retryArgs, &envelope)
	if refusal != nil {
		b.invalidateOnRefusal(refusal)
	}
	return result, envelope, refusal, err
}

func (b *cuaBrowserServer) rebindExact(
	ctx context.Context,
	oldTarget string,
	oldSelected string,
) (*cuaRefusal, error) {
	b.mu.Lock()
	pid, windowID := b.preparedPID, b.windowID
	b.mu.Unlock()
	if pid <= 0 || windowID <= 0 {
		return nil, errors.New("cannot rebind Cua browser without a prepared pid and window")
	}
	var bind cuaResultEnvelope
	refusal, err := b.call(ctx, "get_browser_state", b.sessionArgs(map[string]any{
		"pid": pid, "window_id": windowID,
	}), &bind)
	if err != nil || refusal != nil {
		return refusal, err
	}
	if err := validateExactBinding(bind); err != nil {
		return nil, err
	}
	b.mu.Lock()
	state := b.aliasStateLocked()
	changed := oldTarget == "" || bind.TargetID != oldTarget
	state.observeTarget(bind.TargetID)
	state.syncTabs(bind.Tabs)
	if !changed {
		state.selectedTab = oldSelected
		if state.tabs[oldSelected].ID == "" {
			changed = true
		}
	}
	if changed {
		selected, selectionRefusal := selectUnambiguousTab(bind.Tabs)
		if selectionRefusal != nil {
			b.installAliasStateLocked(state)
			b.mu.Unlock()
			return selectionRefusal, nil
		}
		state.selectedTab = selected
	}
	clearCUATabScopedCapabilities(state)
	b.installAliasStateLocked(state)
	b.mu.Unlock()
	return nil, nil
}

func (b *cuaBrowserServer) validateSelectedSnapshot(envelope cuaResultEnvelope) error {
	b.mu.Lock()
	targetID, tabID := b.targetID, b.selectedTab
	b.mu.Unlock()
	if envelope.TargetID == "" || envelope.TabID == "" {
		return errors.New("Cua semantic snapshot omitted target or tab identity")
	}
	if envelope.TargetID != targetID || envelope.TabID != tabID {
		if envelope.TargetID != "" && envelope.TargetID != targetID {
			b.observeTargetGeneration(envelope.TargetID)
		}
		return errors.New("Cua semantic snapshot returned a different target or tab")
	}
	if envelope.Snapshot.Format != "semantic_v2" {
		return fmt.Errorf("Cua snapshot format is %q, want semantic_v2", envelope.Snapshot.Format)
	}
	if envelope.Snapshot.ID == "" {
		return errors.New("Cua semantic snapshot omitted snapshot id")
	}
	return nil
}

func (b *cuaBrowserServer) observeTargetGeneration(targetID string) {
	b.mu.Lock()
	state := b.aliasStateLocked()
	state.observeTarget(targetID)
	b.installAliasStateLocked(state)
	b.mu.Unlock()
}

func validateSnapshotContinuation(initial, next cuaResultEnvelope) error {
	if initial.Snapshot.ID == "" || next.Snapshot.ID == "" || next.Snapshot.ID != initial.Snapshot.ID {
		return errors.New("Cua snapshot continuation changed snapshot identity")
	}
	if next.TargetID != initial.TargetID {
		return errors.New("Cua snapshot continuation changed target identity")
	}
	if next.TabID != initial.TabID {
		return errors.New("Cua snapshot continuation changed tab identity")
	}
	return nil
}

func (b *cuaBrowserServer) mutateThenSnapshot(
	ctx context.Context,
	tool string,
	args map[string]any,
	summary string,
) (*mcp.CallToolResult, BrowserOutput, error) {
	result, output, _, err := b.mutateThenSnapshotEnvelope(ctx, tool, args, summary)
	return result, output, err
}

func (b *cuaBrowserServer) mutateThenSnapshotEnvelope(
	ctx context.Context,
	tool string,
	args map[string]any,
	summary string,
) (*mcp.CallToolResult, BrowserOutput, cuaResultEnvelope, error) {
	var mutation cuaResultEnvelope
	_, refusal, err := b.callResult(ctx, tool, args, &mutation)
	if err != nil {
		b.clearTabScopedCapabilities()
		return nil, BrowserOutput{}, cuaResultEnvelope{}, b.publicCallError(err, args)
	}
	if refusal != nil {
		b.invalidateOnRefusal(refusal)
		result, output, refusalErr := b.browserRefusalResult(refusal, args)
		return result, output, cuaResultEnvelope{}, refusalErr
	}
	// The mutation completed. Invalidate pre-action capabilities before the
	// verification read so every verification failure remains fail-closed.
	b.clearTabScopedCapabilities()
	result, output, envelope, err := b.snapshotInternal(ctx, false, false, false)
	if err != nil {
		return nil, BrowserOutput{}, cuaResultEnvelope{}, fmt.Errorf(
			"%s succeeded but post-action snapshot failed: %w",
			summary,
			b.publicCallError(err, args),
		)
	}
	if output.Refusal != nil {
		b.mu.Lock()
		output.Refusal = b.aliasStateLocked().resanitizePublicRefusal(output.Refusal, args)
		b.mu.Unlock()
		result = textResult(output.Refusal.Message)
	}
	if output.Status == "ok" {
		result = textResult(output.Snapshot)
	}
	return result, output, envelope, nil
}

func (b *cuaBrowserServer) invalidateOnRefusal(refusal *cuaRefusal) {
	if refusal == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.aliasStateLocked()
	switch refusal.Code {
	case "browser_binding_stale", "browser_reconnect_exhausted":
		state.clearCapabilities()
	case "browser_target_changed":
		state.targetID = ""
		state.clearCapabilities()
	case "browser_tab_stale", "browser_ref_stale", "browser_ref_unknown":
		clearCUATabScopedCapabilities(state)
	}
	b.installAliasStateLocked(state)
}

func isRebindableReadRefusal(code string) bool {
	return code == "browser_binding_stale" || code == "browser_reconnect_exhausted"
}

func (b *cuaBrowserServer) guardNativeKeyTarget(ctx context.Context) (*cuaRefusal, error) {
	b.mu.Lock()
	pid, windowID := b.preparedPID, b.windowID
	targetID, selectedTab := b.targetID, b.selectedTab
	b.mu.Unlock()
	var bind cuaResultEnvelope
	refusal, err := b.call(ctx, "get_browser_state", b.sessionArgs(map[string]any{
		"pid": pid, "window_id": windowID,
	}), &bind)
	if err != nil || refusal != nil {
		if refusal != nil {
			b.invalidateOnRefusal(refusal)
		}
		return refusal, err
	}
	targetChanged := bind.TargetID != "" && bind.TargetID != targetID
	if targetChanged {
		b.observeTargetGeneration(bind.TargetID)
	}
	if err := validateExactBinding(bind); err != nil {
		return nil, err
	}
	if targetChanged {
		return &cuaRefusal{Code: "browser_target_changed", Message: "browser target changed; take a fresh snapshot"}, nil
	}
	selectedCount, activeCount := 0, 0
	selectedActive := false
	for _, tab := range bind.Tabs {
		if tab.Active != nil && *tab.Active {
			activeCount++
		}
		if tab.ID == selectedTab {
			selectedCount++
			selectedActive = tab.Active != nil && *tab.Active
		}
	}
	if selectedCount != 1 || activeCount != 1 || !selectedActive {
		return &cuaRefusal{
			Code:    "browser_native_key_target_ambiguous",
			Message: "selected browser tab is not uniquely active; select or click a page control before pressing a native key",
		}, nil
	}
	return nil, nil
}

func validateScreenshotMetrics(screenshot *cuaScreenshot) error {
	if screenshot == nil || !finitePositive(screenshot.ViewportCSSWidth) || !finitePositive(screenshot.ViewportCSSHeight) {
		return errors.New("Cua screenshot omitted valid positive viewport CSS metrics")
	}
	return nil
}

func validateScreenshotMetadata(screenshot *cuaScreenshot) error {
	if err := validateScreenshotMetrics(screenshot); err != nil {
		return err
	}
	if screenshot.MIMEType != "image/png" || screenshot.Width <= 0 || screenshot.Height <= 0 {
		return errors.New("Cua screenshot metadata is not a non-empty PNG")
	}
	return nil
}

func finitePositive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func (b *cuaBrowserServer) bindingArgs() (map[string]any, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.preparedPID <= 0 || b.windowID <= 0 {
		return nil, fmt.Errorf("no page open — call browser_navigate first")
	}
	return b.sessionArgs(map[string]any{"pid": b.preparedPID, "window_id": b.windowID}), nil
}

func (b *cuaBrowserServer) isPrepared() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.preparedPID > 0 && b.windowID > 0 && b.targetID != "" && b.selectedTab != ""
}

func (b *cuaBrowserServer) requirePrepared() error {
	if !b.isPrepared() {
		return fmt.Errorf("no page open — call browser_navigate first")
	}
	return nil
}

func (b *cuaBrowserServer) supportedApps(apps []cuaBrowserApp) []cuaBrowserApp {
	explicit := normalizedExecutableIdentity(b.cfg.Browser.ChromePath)
	supported := make([]cuaBrowserApp, 0, len(apps))
	for _, app := range apps {
		if !isSupportedBrowserName(app.Name) {
			continue
		}
		if explicit != "" && !appMatchesExecutable(app, explicit) {
			continue
		}
		supported = append(supported, app)
	}
	return supported
}

func isSupportedBrowserName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.Contains(name, "chrome") || strings.Contains(name, "chromium") ||
		strings.Contains(name, "microsoft edge") || name == "edge" || strings.Contains(name, "msedge")
}

func normalizedExecutableIdentity(path string) string {
	if runtime.GOOS != "windows" {
		return path
	}
	return strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
}

func appMatchesExecutable(app cuaBrowserApp, explicit string) bool {
	for _, candidate := range []string{app.LaunchPath, app.ExecutablePath, app.Path} {
		if normalizedExecutableIdentity(candidate) == explicit {
			return true
		}
	}
	return false
}

func pickRunningBrowser(apps []cuaBrowserApp, windows []cuaWindow, explicitExecutable string) (int, int) {
	for _, app := range apps {
		byPID := make(map[int][]cuaWindow)
		order := make([]int, 0)
		for _, window := range windows {
			if window.PID <= 0 || window.WindowID <= 0 || !windowMatchesApp(window, app, explicitExecutable) {
				continue
			}
			if _, exists := byPID[window.PID]; !exists {
				order = append(order, window.PID)
			}
			byPID[window.PID] = append(byPID[window.PID], window)
		}
		for _, pid := range order {
			if len(byPID[pid]) == 1 {
				return pid, byPID[pid][0].WindowID
			}
		}
	}
	return 0, 0
}

func windowMatchesApp(window cuaWindow, app cuaBrowserApp, explicitExecutable string) bool {
	appPaths := []string{app.LaunchPath, app.ExecutablePath, app.Path}
	windowPaths := []string{window.LaunchPath, window.Path}
	for _, appPath := range appPaths {
		if appPath == "" {
			continue
		}
		for _, windowPath := range windowPaths {
			if normalizedExecutableIdentity(appPath) == normalizedExecutableIdentity(windowPath) {
				return true
			}
		}
	}
	// An explicit chrome_path is an executable-identity constraint, not merely
	// an app-name preference. If the native window cannot prove that same path,
	// do not reuse it; launch the exact listed app instead.
	if explicitExecutable != "" {
		return false
	}
	windowIdentity := strings.ToLower(strings.Join([]string{window.App, window.Name, window.Title}, " "))
	return strings.Contains(windowIdentity, strings.ToLower(strings.TrimSpace(app.Name))) ||
		(isSupportedBrowserName(windowIdentity) && isSupportedBrowserName(app.Name))
}

func (b *cuaBrowserServer) launchArgs(app cuaBrowserApp) (map[string]any, error) {
	if app.LaunchPath != "" {
		return map[string]any{"launch_path": app.LaunchPath}, nil
	}
	if runtime.GOOS == "windows" && app.Path != "" {
		return map[string]any{"path": app.Path}, nil
	}
	if app.BundleID != "" {
		return map[string]any{"bundle_id": app.BundleID}, nil
	}
	if app.Name != "" {
		return map[string]any{"name": app.Name}, nil
	}
	return nil, errors.New("selected browser app has no round-trippable launch identity")
}

func (b *cuaBrowserServer) waitForExactWindow(ctx context.Context, pid int) (int, *cuaRefusal, error) {
	for {
		var result cuaResultEnvelope
		refusal, err := b.call(ctx, "list_windows", b.sessionArgs(nil), &result)
		if err != nil || refusal != nil {
			return 0, refusal, err
		}
		windows := make([]cuaWindow, 0, 1)
		for _, window := range result.Windows {
			if window.PID == pid && window.WindowID > 0 {
				windows = append(windows, window)
			}
		}
		if len(windows) == 1 {
			return windows[0].WindowID, nil, nil
		}
		if len(windows) > 1 {
			return 0, nil, fmt.Errorf("Cua reported multiple native windows for pid %d; exact binding is ambiguous", pid)
		}
		timer := time.NewTimer(b.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, nil, fmt.Errorf("wait for Cua browser window: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func validateExactBinding(bind cuaResultEnvelope) error {
	if bind.BindingQuality != "exact" {
		return fmt.Errorf("Cua browser binding quality is %q, want exact", bind.BindingQuality)
	}
	if !bind.MutationAllowed {
		return errors.New("Cua exact browser binding does not allow mutation")
	}
	if bind.TargetID == "" {
		return errors.New("Cua exact browser binding omitted target_id")
	}
	return nil
}

func selectUnambiguousTab(tabs []cuaTab) (string, *cuaRefusal) {
	valid := make([]cuaTab, 0, len(tabs))
	for _, tab := range tabs {
		if tab.ID != "" {
			valid = append(valid, tab)
		}
	}
	if len(valid) == 1 {
		return valid[0].ID, nil
	}
	active := ""
	activeCount := 0
	for _, tab := range valid {
		if tab.Active != nil && *tab.Active {
			active = tab.ID
			activeCount++
		}
	}
	if activeCount == 1 {
		return active, nil
	}
	return "", &cuaRefusal{
		Code:    "browser_tab_ambiguous",
		Message: "browser binding has no uniquely active tab; close extra tabs or make one tab active and retry browser_navigate",
	}
}

func (b *cuaBrowserServer) killOwnedSource(ctx context.Context, pid int) error {
	cleanupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var result cuaResultEnvelope
	refusal, err := b.call(cleanupCtx, "kill_app", b.sessionArgs(map[string]any{"pid": pid}), &result)
	if err != nil {
		return fmt.Errorf("clean up temporary Cua browser source: %w", err)
	}
	if refusal != nil {
		return errors.New("clean up temporary Cua browser source: Cua refused cleanup")
	}
	return nil
}

func (b *cuaBrowserServer) aliasStateLocked() *cuaAliasState {
	return &cuaAliasState{
		targetID:     b.targetID,
		tabs:         b.tabs,
		tabAliases:   b.tabAliases,
		tabReverse:   b.tabReverse,
		nextTabAlias: b.nextTabAlias,
		selectedTab:  b.selectedTab,
		refs:         b.refs,
		lastEditable: b.lastEditable,
		dialogID:     b.dialogID,
	}
}

func (b *cuaBrowserServer) installAliasStateLocked(state *cuaAliasState) {
	b.targetID = state.targetID
	b.tabs = state.tabs
	b.tabAliases = state.tabAliases
	b.tabReverse = state.tabReverse
	b.nextTabAlias = state.nextTabAlias
	b.selectedTab = state.selectedTab
	b.refs = state.refs
	b.lastEditable = state.lastEditable
	b.dialogID = state.dialogID
	if state.dialogID == "" {
		b.dialogKind = ""
	}
}

func clearCUATabScopedCapabilities(state *cuaAliasState) {
	state.refs = make(map[string]cuaElement)
	state.lastEditable = ""
	state.dialogID = ""
}

func (b *cuaBrowserServer) browserRefusalResult(
	refusal *cuaRefusal,
	args map[string]any,
) (*mcp.CallToolResult, BrowserOutput, error) {
	b.mu.Lock()
	public := b.aliasStateLocked().publicRefusal(refusal, args)
	b.mu.Unlock()
	output := BrowserOutput{BrowserOutcome: BrowserOutcome{Status: "refused", Refusal: public}}
	return textResult(public.Message), output, nil
}

func (b *cuaBrowserServer) tabsRefusalResult(
	refusal *cuaRefusal,
	args map[string]any,
) (*mcp.CallToolResult, BrowserTabsOutput, error) {
	b.mu.Lock()
	public := b.aliasStateLocked().publicRefusal(refusal, args)
	b.mu.Unlock()
	output := BrowserTabsOutput{BrowserOutcome: BrowserOutcome{Status: "refused", Refusal: public}}
	return textResult(public.Message), output, nil
}

func (b *cuaBrowserServer) tabsOutputLocked(tabs []cuaTab) BrowserTabsOutput {
	output := BrowserTabsOutput{BrowserOutcome: BrowserOutcome{Status: "ok"}}
	for _, tab := range tabs {
		alias := b.tabReverse[tab.ID]
		if alias == "" {
			continue
		}
		output.Tabs = append(output.Tabs, BrowserTabOutput{
			ID: alias, Title: tab.Title, URL: tab.URL, Active: tab.Active,
			Selected: tab.ID == b.selectedTab,
		})
	}
	return output
}

func renderBrowserTabs(tabs []BrowserTabOutput) string {
	if len(tabs) == 0 {
		return "no browser tabs"
	}
	lines := make([]string, 0, len(tabs))
	for _, tab := range tabs {
		markers := make([]string, 0, 2)
		if tab.Selected {
			markers = append(markers, "selected")
		}
		if tab.Active != nil && *tab.Active {
			markers = append(markers, "active")
		}
		marker := ""
		if len(markers) != 0 {
			marker = " [" + strings.Join(markers, ",") + "]"
		}
		lines = append(lines, fmt.Sprintf("%s %q %s%s", tab.ID, tab.Title, tab.URL, marker))
	}
	return strings.Join(lines, "\n")
}

func startCUABrowserMCPServer(
	ctx context.Context,
	transport mcp.Transport,
	cfg types.Config,
	runtime *cuaRuntime,
) error {
	if runtime == nil {
		return errors.New("Cua browser backend requires a shared runtime")
	}
	bs := newCUABrowserServer(runtime, cfg)
	server := mcp.NewServer(&mcp.Implementation{Name: "browser", Version: "v1.0.0"}, nil)
	registerBrowserTools(server, browserToolHandlers{
		navigate: bs.browserNavigate,
		snapshot: bs.browserSnapshot,
		click:    bs.browserClick,
		typeText: bs.browserType,
		press:    bs.browserPress,
		scroll:   bs.browserScroll,
		vision:   bs.browserVision,
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "browser_tabs",
		Description: "List logical tabs for the exact prepared Cua browser target without activating them.",
	}, bs.browserTabs)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "browser_select_tab",
		Description: "Select a current logical @tN tab without native activation and return its semantic snapshot.",
	}, bs.browserSelectTab)
	registerCUABrowserAdvancedTools(server, bs)
	registerCUABrowserFileTools(server, bs)

	xlog.Info("Cua browser MCP server ready")
	return server.Run(ctx, transport)
}
