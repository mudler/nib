//go:build cuabrowserintegration

package mcp

// Run on a GUI host with Cua Driver 0.19.0+ installed:
//
//	go test -tags cuabrowserintegration ./mcp -run CUABrowserIntegration -v

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/types"
)

const (
	liveCUABrowserTimeout    = 3 * time.Minute
	liveCUATabPollTimeout    = 10 * time.Second
	liveCUATabPollInterval   = 100 * time.Millisecond
	liveCUAUploadName        = "upload.txt"
	liveCUADownloadDirectory = "downloads"
	liveCUAAttachment        = "cua live attachment\n"
)

type liveCUABrowserFixture struct {
	ctx       context.Context
	browser   *cuaBrowserServer
	serverURL string
	workDir   string
}

func TestLiveCUATabPollingWaitsForExpectedTitleAndURL(t *testing.T) {
	const (
		wantTitle = "Cua second tab"
		wantURL   = "http://127.0.0.1/second"
	)
	states := []BrowserTabsOutput{
		{BrowserOutcome: BrowserOutcome{Status: "ok"}, Tabs: []BrowserTabOutput{{ID: "@t2", URL: "about:blank"}}},
		{BrowserOutcome: BrowserOutcome{Status: "ok"}, Tabs: []BrowserTabOutput{{ID: "@t2", Title: wantTitle, URL: "about:blank"}}},
		{BrowserOutcome: BrowserOutcome{Status: "ok"}, Tabs: []BrowserTabOutput{{ID: "@t2", Title: wantTitle, URL: wantURL}}},
	}
	calls := 0
	got, err := pollLiveCUABrowserTab(t.Context(), time.Nanosecond, wantTitle, wantURL, func(context.Context) (BrowserTabsOutput, error) {
		state := states[calls]
		calls++
		return state, nil
	})
	if err != nil {
		t.Fatalf("pollLiveCUABrowserTab: %v", err)
	}
	if got != "@t2" || calls != 3 {
		t.Fatalf("pollLiveCUABrowserTab = (%q, %d calls), want (@t2, 3 calls)", got, calls)
	}
}

func TestLiveCUATabPollingTimeoutIncludesLastState(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	last := BrowserTabsOutput{
		BrowserOutcome: BrowserOutcome{Status: "ok"},
		Tabs:           []BrowserTabOutput{{ID: "@t2", Title: "Loading", URL: "about:blank"}},
	}
	_, err := pollLiveCUABrowserTab(ctx, time.Nanosecond, "Cua second tab", "http://127.0.0.1/second", func(context.Context) (BrowserTabsOutput, error) {
		cancel()
		return last, nil
	})
	if err == nil || !strings.Contains(err.Error(), "Loading") || !strings.Contains(err.Error(), "about:blank") {
		t.Fatalf("pollLiveCUABrowserTab timeout error = %v, want last title and URL diagnostics", err)
	}
}

// TestCUABrowserIntegrationPrepareNavigateAndSnapshot proves that the live
// adapter prepares a real Cua-owned browser, navigates to a local URL, and
// returns semantic refs through nib's compact snapshot contract.
func TestCUABrowserIntegrationPrepareNavigateAndSnapshot(t *testing.T) {
	fixture := newLiveCUABrowserFixture(t)

	out := fixture.navigate(t, "/")
	requireLiveCUAStatus(t, out.BrowserOutcome)
	requireLiveCUASnapshot(t, out.Snapshot, "fixture ready")
	if ref := liveCUARefForName(t, out.Snapshot, "Query box"); ref == "" {
		t.Fatal("live snapshot did not expose the query input through a compact ref")
	}

	_, refreshed, err := fixture.browser.browserSnapshot(fixture.ctx, nil, BrowserInput{Full: true})
	if err != nil {
		t.Fatalf("browser_snapshot: %v", err)
	}
	requireLiveCUAStatus(t, refreshed.BrowserOutcome)
	requireLiveCUASnapshot(t, refreshed.Snapshot, "Download attachment")
}

// TestCUABrowserIntegrationTypeAndEnter catches a browser_type implementation
// that does not reach the DOM or fails to preserve exact-ref focus for Enter.
func TestCUABrowserIntegrationTypeAndEnter(t *testing.T) {
	fixture := newLiveCUABrowserFixture(t)
	out := fixture.navigate(t, "/")

	queryRef := liveCUARefForName(t, out.Snapshot, "Query box")
	_, typed, err := fixture.browser.browserType(fixture.ctx, nil, BrowserInput{
		Ref: queryRef, Text: "live query",
	})
	if err != nil {
		t.Fatalf("browser_type: %v", err)
	}
	requireLiveCUAStatus(t, typed.BrowserOutcome)
	requireLiveCUASnapshot(t, typed.Snapshot, "live query")

	_, submitted, err := fixture.browser.browserPress(fixture.ctx, nil, BrowserInput{Key: "Enter"})
	if err != nil {
		t.Fatalf("browser_press Enter: %v", err)
	}
	requireLiveCUAStatus(t, submitted.BrowserOutcome)
	requireLiveCUASnapshot(t, submitted.Snapshot, "submitted: live query")
}

// TestCUABrowserIntegrationPointerAndTabs exercises every pointer shape in the
// approved fixture, then opens and logically selects a second real browser tab.
func TestCUABrowserIntegrationPointerAndTabs(t *testing.T) {
	fixture := newLiveCUABrowserFixture(t)
	out := fixture.navigate(t, "/")

	hoverRef := liveCUARefForName(t, out.Snapshot, "Hover target")
	_, out, err := fixture.browser.browserPointer(fixture.ctx, nil, BrowserPointerInput{
		Action: "hover", Ref: hoverRef,
	})
	if err != nil {
		t.Fatalf("browser_pointer hover: %v", err)
	}
	requireLiveCUAStatus(t, out.BrowserOutcome)
	requireLiveCUASnapshot(t, out.Snapshot, "hovered: yes")

	doubleRef := liveCUARefForName(t, out.Snapshot, "Double-click target")
	_, out, err = fixture.browser.browserPointer(fixture.ctx, nil, BrowserPointerInput{
		Action: "double_click", Ref: doubleRef,
	})
	if err != nil {
		t.Fatalf("browser_pointer double_click: %v", err)
	}
	requireLiveCUAStatus(t, out.BrowserOutcome)
	requireLiveCUASnapshot(t, out.Snapshot, "double-clicked: yes")

	dragRef := liveCUARefForName(t, out.Snapshot, "Drag source")
	dropRef := liveCUARefForName(t, out.Snapshot, "Drop target")
	_, out, err = fixture.browser.browserPointer(fixture.ctx, nil, BrowserPointerInput{
		Action: "drag", Ref: dragRef, DestinationRef: dropRef,
	})
	if err != nil {
		t.Fatalf("browser_pointer drag: %v", err)
	}
	requireLiveCUAStatus(t, out.BrowserOutcome)
	requireLiveCUASnapshot(t, out.Snapshot, "dropped: yes")

	openTabRef := liveCUARefForName(t, out.Snapshot, "Open second tab")
	_, out, err = fixture.browser.browserClick(fixture.ctx, nil, BrowserInput{Ref: openTabRef})
	if err != nil {
		t.Fatalf("browser_click second-tab link: %v", err)
	}
	requireLiveCUAStatus(t, out.BrowserOutcome)
	requireLiveCUASnapshot(t, out.Snapshot, "tab opened: yes")

	deltaY := float64(900)
	x, y := float64(200), float64(200)
	_, out, err = fixture.browser.browserPointer(fixture.ctx, nil, BrowserPointerInput{
		Action: "scroll", X: &x, Y: &y, DeltaY: &deltaY,
	})
	if err != nil {
		t.Fatalf("browser_pointer scroll: %v", err)
	}
	requireLiveCUAStatus(t, out.BrowserOutcome)
	requireLiveCUASnapshot(t, out.Snapshot, "scrolled: yes")

	secondTab := fixture.waitForTab(t, "Cua second tab", "/second")
	_, selected, err := fixture.browser.browserSelectTab(fixture.ctx, nil, BrowserSelectTabInput{TabID: secondTab})
	if err != nil {
		t.Fatalf("browser_select_tab: %v", err)
	}
	requireLiveCUAStatus(t, selected.BrowserOutcome)
	requireLiveCUASnapshot(t, selected.Snapshot, "second tab ready")
}

// TestCUABrowserIntegrationFileDialogAndDownload assigns only a test-owned
// upload and downloads only a same-origin attachment into the temporary root.
func TestCUABrowserIntegrationFileDialogAndDownload(t *testing.T) {
	fixture := newLiveCUABrowserFixture(t)
	out := fixture.navigate(t, "/")

	fileRef := liveCUARefForName(t, out.Snapshot, "Upload fixture")
	_, uploaded, err := fixture.browser.browserSetInputFiles(fixture.ctx, nil, BrowserSetInputFilesInput{
		Ref: fileRef, Files: []string{liveCUAUploadName},
	})
	if err != nil {
		t.Fatalf("browser_set_input_files: %v", err)
	}
	requireLiveCUAStatus(t, uploaded.BrowserOutcome)
	if uploaded.AssignedCount != 1 {
		t.Fatalf("browser_set_input_files assigned count = %d, want 1", uploaded.AssignedCount)
	}
	requireLiveCUASnapshot(t, uploaded.Snapshot, "selected files: 1")

	snapshot := fixture.resolveDialog(t, uploaded.Snapshot, liveCUADialogCase{
		trigger: "Alert trigger", kind: "alert", action: "accept",
		opened: "alert opened", resolved: "alert accepted",
	})
	snapshot = fixture.resolveDialog(t, snapshot, liveCUADialogCase{
		trigger: "Confirm trigger", kind: "confirm", action: "dismiss",
		opened: "confirm opened", resolved: "confirm dismissed",
	})
	promptText := "live prompt answer"
	snapshot = fixture.resolveDialog(t, snapshot, liveCUADialogCase{
		trigger: "Prompt trigger", kind: "prompt", action: "accept", promptText: &promptText,
		opened: "prompt opened", resolved: "prompt: live prompt answer",
	})

	downloadRef := liveCUARefForName(t, snapshot, "Download attachment")
	_, downloaded, err := fixture.browser.browserDownload(fixture.ctx, nil, BrowserDownloadInput{
		Ref: downloadRef, Directory: liveCUADownloadDirectory,
	})
	if err != nil {
		t.Fatalf("browser_download: %v", err)
	}
	requireLiveCUAStatus(t, downloaded.BrowserOutcome)
	if downloaded.Bytes != int64(len(liveCUAAttachment)) {
		t.Fatalf("browser_download bytes = %d, want %d", downloaded.Bytes, len(liveCUAAttachment))
	}
	requireLiveCUASnapshot(t, downloaded.Snapshot, "download requested: yes")
	requireLiveCUADownload(t, filepath.Join(fixture.workDir, liveCUADownloadDirectory))
}

// TestCUABrowserIntegrationVisionUsesExactTab leaves the second tab logically
// selected without activating it and verifies that vision returns its blue
// viewport rather than the red first-tab viewport.
func TestCUABrowserIntegrationVisionUsesExactTab(t *testing.T) {
	fixture := newLiveCUABrowserFixture(t)
	out := fixture.navigate(t, "/")

	_, out, err := fixture.browser.browserClick(fixture.ctx, nil, BrowserInput{
		Ref: liveCUARefForName(t, out.Snapshot, "Open second tab"),
	})
	if err != nil {
		t.Fatalf("browser_click second-tab link: %v", err)
	}
	requireLiveCUAStatus(t, out.BrowserOutcome)
	requireLiveCUASnapshot(t, out.Snapshot, "tab opened: yes")

	secondTab := fixture.waitForTab(t, "Cua second tab", "/second")
	_, selected, err := fixture.browser.browserSelectTab(fixture.ctx, nil, BrowserSelectTabInput{TabID: secondTab})
	if err != nil {
		t.Fatalf("browser_select_tab: %v", err)
	}
	requireLiveCUAStatus(t, selected.BrowserOutcome)
	requireLiveCUASnapshot(t, selected.Snapshot, "second tab ready")

	result, _, err := fixture.browser.browserVision(fixture.ctx, nil, BrowserInput{Question: "verify the blue second tab"})
	if err != nil {
		t.Fatalf("browser_vision: %v", err)
	}
	liveCUARequireBlueScreenshot(t, result)
}

func newLiveCUABrowserFixture(t *testing.T) *liveCUABrowserFixture {
	t.Helper()
	driver := requireLiveCUABrowserPrerequisites(t)

	ctx, cancel := context.WithTimeout(t.Context(), liveCUABrowserTimeout)
	t.Cleanup(cancel)
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, liveCUAUploadName), []byte("cua live upload\n"), 0o600); err != nil {
		t.Fatalf("create upload fixture: %v", err)
	}
	if err := os.Mkdir(filepath.Join(workDir, liveCUADownloadDirectory), 0o700); err != nil {
		t.Fatalf("create download fixture directory: %v", err)
	}

	server := httptest.NewServer(liveCUABrowserHandler())
	t.Cleanup(server.Close)
	unique := fmt.Sprintf("nib-live-%d", time.Now().UnixNano())
	cfg := types.Config{
		WorkingDir: workDir,
		CUA: types.CUAConfig{
			Command: driver, SessionID: unique,
		},
		Browser: types.BrowserConfig{
			Enabled: true, Backend: "cua",
			AllowPrivateURLs: true, ProfileName: unique,
		},
	}
	liveRuntime, err := newCUARuntime(ctx, cfg, true)
	if err != nil {
		t.Fatalf("start live Cua runtime: %v", err)
	}
	t.Cleanup(func() {
		if err := liveRuntime.Close(); err != nil {
			t.Errorf("close live Cua runtime: %v", err)
		}
	})

	return &liveCUABrowserFixture{
		ctx: ctx, browser: newCUABrowserServer(liveRuntime, cfg), serverURL: server.URL, workDir: workDir,
	}
}

func requireLiveCUABrowserPrerequisites(t *testing.T) string {
	t.Helper()
	driver := strings.TrimSpace(os.Getenv("NIB_CUA_DRIVER_CMD"))
	if driver == "" {
		driver = "cua-driver"
	}
	resolvedDriver, err := exec.LookPath(driver)
	if err != nil {
		t.Skipf("live Cua browser integration unavailable: Cua Driver 0.19.0+ executable %q was not found", driver)
	}
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("live Cua browser integration unavailable: neither DISPLAY nor WAYLAND_DISPLAY is set")
	}
	if discoverChrome("") == "" {
		t.Skip("live Cua browser integration unavailable: Chrome, Chromium, or Edge was not found")
	}
	return resolvedDriver
}

func (fixture *liveCUABrowserFixture) navigate(t *testing.T, path string) BrowserOutput {
	t.Helper()
	_, out, err := fixture.browser.browserNavigate(fixture.ctx, nil, BrowserInput{URL: fixture.serverURL + path})
	if err != nil {
		t.Fatalf("browser_navigate %s: %v", path, err)
	}
	return out
}

func (fixture *liveCUABrowserFixture) waitForTab(t *testing.T, title, path string) string {
	t.Helper()
	pollCtx, cancel := context.WithTimeout(fixture.ctx, liveCUATabPollTimeout)
	defer cancel()
	tabID, err := pollLiveCUABrowserTab(
		pollCtx,
		liveCUATabPollInterval,
		title,
		fixture.serverURL+path,
		func(ctx context.Context) (BrowserTabsOutput, error) {
			_, tabs, err := fixture.browser.browserTabs(ctx, nil, BrowserTabsInput{})
			return tabs, err
		},
	)
	if err != nil {
		t.Fatalf("wait for browser tab: %v", err)
	}
	return tabID
}

func pollLiveCUABrowserTab(
	ctx context.Context,
	interval time.Duration,
	title string,
	url string,
	list func(context.Context) (BrowserTabsOutput, error),
) (string, error) {
	var last BrowserTabsOutput
	for {
		if err := ctx.Err(); err != nil {
			return "", liveCUATabPollError(err, title, url, last.Tabs)
		}
		tabs, err := list(ctx)
		if err != nil {
			return "", fmt.Errorf("poll browser_tabs: %w; last browser tabs: %s", err, renderBrowserTabs(last.Tabs))
		}
		last = tabs
		if tabs.Status != "ok" {
			return "", fmt.Errorf("poll browser_tabs returned status %q with refusal %#v; last browser tabs: %s", tabs.Status, tabs.Refusal, renderBrowserTabs(tabs.Tabs))
		}
		for _, tab := range tabs.Tabs {
			if tab.Title == title && tab.URL == url {
				return tab.ID, nil
			}
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", liveCUATabPollError(ctx.Err(), title, url, last.Tabs)
		case <-timer.C:
		}
	}
}

func liveCUATabPollError(err error, title, url string, tabs []BrowserTabOutput) error {
	return fmt.Errorf(
		"wait for browser tab title %q and URL %q: %w; last browser tabs: %s",
		title,
		url,
		err,
		renderBrowserTabs(tabs),
	)
}

type liveCUADialogCase struct {
	trigger    string
	kind       string
	action     string
	promptText *string
	opened     string
	resolved   string
}

func (fixture *liveCUABrowserFixture) resolveDialog(t *testing.T, snapshot string, dialog liveCUADialogCase) string {
	t.Helper()
	_, clicked, err := fixture.browser.browserClick(fixture.ctx, nil, BrowserInput{
		Ref: liveCUARefForName(t, snapshot, dialog.trigger),
	})
	if err != nil {
		t.Fatalf("browser_click %s: %v", dialog.trigger, err)
	}
	requireLiveCUAStatus(t, clicked.BrowserOutcome)
	requireLiveCUASnapshot(t, clicked.Snapshot, dialog.opened)

	_, inspected, err := fixture.browser.browserDialog(fixture.ctx, nil, BrowserDialogInput{Action: "inspect"})
	if err != nil {
		t.Fatalf("browser_dialog inspect %s: %v", dialog.kind, err)
	}
	requireLiveCUAStatus(t, inspected.BrowserOutcome)
	if !inspected.Present || inspected.Kind != dialog.kind || inspected.DialogID == "" {
		t.Fatalf("browser_dialog inspect = %#v, want present %s dialog", inspected, dialog.kind)
	}

	_, resolved, err := fixture.browser.browserDialog(fixture.ctx, nil, BrowserDialogInput{
		Action: dialog.action, DialogID: inspected.DialogID, PromptText: dialog.promptText,
	})
	if err != nil {
		t.Fatalf("browser_dialog %s %s: %v", dialog.action, dialog.kind, err)
	}
	requireLiveCUAStatus(t, resolved.BrowserOutcome)
	requireLiveCUASnapshot(t, resolved.Snapshot, dialog.resolved)
	return resolved.Snapshot
}

func requireLiveCUAStatus(t *testing.T, outcome BrowserOutcome) {
	t.Helper()
	if outcome.Status != "ok" {
		t.Fatalf("live Cua browser status = %q, refusal = %#v, want ok", outcome.Status, outcome.Refusal)
	}
}

func requireLiveCUASnapshot(t *testing.T, snapshot, marker string) {
	t.Helper()
	if !strings.Contains(snapshot, marker) {
		t.Fatalf("live snapshot missing %q:\n%s", marker, snapshot)
	}
}

func liveCUARefForName(t *testing.T, snapshot, name string) string {
	t.Helper()
	quotedName := fmt.Sprintf("%q", name)
	for _, line := range strings.Split(snapshot, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 0 && strings.HasPrefix(fields[0], "@e") && strings.Contains(line, quotedName) {
			return fields[0]
		}
	}
	t.Fatalf("live snapshot has no compact ref named %q:\n%s", name, snapshot)
	return ""
}

func requireLiveCUADownload(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read live download directory: %v", err)
	}
	if len(entries) != 1 || entries[0].IsDir() {
		t.Fatalf("live download directory entries = %#v, want one regular file", entries)
	}
	data, err := os.ReadFile(filepath.Join(directory, entries[0].Name()))
	if err != nil {
		t.Fatalf("read live download: %v", err)
	}
	if string(data) != liveCUAAttachment {
		t.Fatalf("live download content = %q, want %q", data, liveCUAAttachment)
	}
}

func liveCUARequireBlueScreenshot(t *testing.T, result *mcp.CallToolResult) {
	t.Helper()
	if result == nil {
		t.Fatal("browser_vision returned no result")
	}
	var screenshot *mcp.ImageContent
	for _, content := range result.Content {
		if image, ok := content.(*mcp.ImageContent); ok {
			screenshot = image
		}
	}
	if screenshot == nil || screenshot.MIMEType != "image/png" || len(screenshot.Data) == 0 {
		t.Fatalf("browser_vision image = %#v, want a non-empty PNG", screenshot)
	}
	decoded, err := png.Decode(bytes.NewReader(screenshot.Data))
	if err != nil {
		t.Fatalf("decode browser_vision PNG: %v", err)
	}
	bounds := decoded.Bounds()
	r, g, b, _ := decoded.At(bounds.Min.X+bounds.Dx()/2, bounds.Min.Y+bounds.Dy()/2).RGBA()
	if b <= r+10_000 || b <= g+10_000 {
		t.Fatalf("browser_vision center pixel = rgb(%d,%d,%d), want the selected blue second tab", r, g, b)
	}
}

func liveCUABrowserHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, liveCUAMainPage)
	})
	mux.HandleFunc("/submitted", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><title>Cua submitted fixture</title><body><main aria-label="submitted: %s">submitted: %s</main></body>`,
			r.URL.Query().Get("q"), r.URL.Query().Get("q"))
	})
	mux.HandleFunc("/second", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><title>Cua second tab</title><style>html,body{height:100%;margin:0;background:rgb(20,120,220)}</style><main aria-label="second tab ready">second tab ready</main>`)
	})
	mux.HandleFunc("/attachment", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="fixture-download.txt"`)
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, liveCUAAttachment)
	})
	return mux
}

const liveCUAMainPage = `<!doctype html>
<html>
<head>
  <title>Cua integration fixture</title>
  <style>
    html, body { min-height: 2600px; margin: 0; background: rgb(220, 30, 30); }
    main { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 8px; padding: 24px; }
    button, input, a { display: block; margin: 0; padding: 8px; }
    p { margin: 0; padding: 8px; background: white; }
    #scroll-state { position: fixed; right: 20px; top: 20px; background: white; }
  </style>
</head>
<body>
<main>
  <p aria-label="fixture ready">fixture ready</p>
  <form action="/submitted" method="get"><input name="q" aria-label="Query box"></form>
  <button aria-label="Hover target" onmouseenter="document.getElementById('hover-state').textContent='hovered: yes'">Hover target</button>
  <p id="hover-state" aria-live="polite">hovered: no</p>
  <button aria-label="Double-click target" ondblclick="document.getElementById('double-state').textContent='double-clicked: yes'">Double-click target</button>
  <p id="double-state" aria-live="polite">double-clicked: no</p>
  <button aria-label="Scroll target">Scroll target</button>
  <p id="scroll-state" aria-live="polite">scrolled: no</p>
  <button aria-label="Drag source" draggable="true" ondragstart="event.dataTransfer.setData('text/plain','fixture')">Drag source</button>
  <button aria-label="Drop target" ondragover="event.preventDefault()" ondrop="event.preventDefault(); document.getElementById('drag-state').textContent='dropped: yes'">Drop target</button>
  <p id="drag-state" aria-live="polite">dropped: no</p>
  <a aria-label="Open second tab" href="/second" target="_blank" onclick="document.getElementById('tab-state').textContent='tab opened: yes'">Open second tab</a>
  <p id="tab-state" aria-live="polite">tab opened: no</p>
  <input aria-label="Upload fixture" type="file" onchange="document.getElementById('file-state').textContent='selected files: '+this.files.length">
  <p id="file-state" aria-live="polite">selected files: 0</p>
  <button aria-label="Alert trigger" onclick="document.getElementById('dialog-state').textContent='alert opened'; alert('fixture alert'); document.getElementById('dialog-state').textContent='alert accepted'">Alert trigger</button>
  <button aria-label="Confirm trigger" onclick="document.getElementById('dialog-state').textContent='confirm opened'; document.getElementById('dialog-state').textContent=confirm('fixture confirm')?'confirm accepted':'confirm dismissed'">Confirm trigger</button>
  <button aria-label="Prompt trigger" onclick="document.getElementById('dialog-state').textContent='prompt opened'; document.getElementById('dialog-state').textContent='prompt: '+prompt('fixture prompt','')">Prompt trigger</button>
  <p id="dialog-state" aria-live="polite">dialog idle</p>
  <a aria-label="Download attachment" href="/attachment" onclick="document.getElementById('download-state').textContent='download requested: yes'">Download attachment</a>
  <p id="download-state" aria-live="polite">download requested: no</p>
</main>
<script>addEventListener('scroll', () => document.getElementById('scroll-state').textContent='scrolled: yes', {once:true})</script>
</body>
</html>`
