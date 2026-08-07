package mcp

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/nib/types"
)

func newTransportRuntimeFactory(
	t *testing.T,
	fake *fakeCUADriver,
	calls *int,
	requireBrowserCalls *[]bool,
) cuaRuntimeFactory {
	t.Helper()
	return func(ctx context.Context, cfg types.Config, requireBrowser bool) (*cuaRuntime, error) {
		*calls = *calls + 1
		*requireBrowserCalls = append(*requireBrowserCalls, requireBrowser)
		return newCUARuntimeFromClient(ctx, fake.Session, cfg, requireBrowser)
	}
}

func startTransportFakeCUA(t *testing.T) (*fakeCUADriver, <-chan struct{}) {
	t.Helper()
	ended := make(chan struct{}, 1)
	fake := startFakeCUA(t, context.Background(), "0.19.0", runtimeBrowserToolNames(), map[string]fakeCUAHandler{
		"end_session": func(map[string]any) (*mcp.CallToolResult, map[string]any, error) {
			ended <- struct{}{}
			return cuaOK(nil)
		},
	})
	return fake, ended
}

func waitForTransportRuntimeClose(t *testing.T, ended <-chan struct{}) {
	t.Helper()
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for shared Cua runtime cleanup")
	}
}

func countFakeCUACalls(calls []fakeCUACall, name string) int {
	count := 0
	for _, call := range calls {
		if call.Name == name {
			count++
		}
	}
	return count
}

func TestStartTransportsComputerOnlyCreatesOneCUARuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fake, ended := startTransportFakeCUA(t)
	factoryCalls := 0
	requireBrowserCalls := []bool{}

	transports, err := startTransports(ctx, types.Config{
		WorkingDir: t.TempDir(),
		CUA:        types.CUAConfig{SessionID: "computer-only"},
		Computer:   types.ComputerConfig{Enabled: true},
	}, nil, newTransportRuntimeFactory(t, fake, &factoryCalls, &requireBrowserCalls))
	if err != nil {
		t.Fatal(err)
	}
	if len(transports) != 4 {
		t.Fatalf("transport count = %d, want three built-ins plus computer", len(transports))
	}
	if factoryCalls != 1 {
		t.Fatalf("Cua runtime factory calls = %d, want 1", factoryCalls)
	}
	if !reflect.DeepEqual(requireBrowserCalls, []bool{false}) {
		t.Fatalf("browser requirements = %v, want [false]", requireBrowserCalls)
	}
	if calls := fake.Calls(); countFakeCUACalls(calls, "start_session") != 1 {
		t.Fatalf("start_session calls = %#v, want exactly one", calls)
	}

	cancel()
	waitForTransportRuntimeClose(t, ended)
	if calls := fake.Calls(); countFakeCUACalls(calls, "end_session") != 1 {
		t.Fatalf("end_session calls = %#v, want exactly one", calls)
	}
}

func TestStartTransportsCUABrowserOnlyCreatesOneCUARuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fake, ended := startTransportFakeCUA(t)
	factoryCalls := 0
	requireBrowserCalls := []bool{}

	transports, err := startTransports(ctx, types.Config{
		WorkingDir: t.TempDir(),
		CUA:        types.CUAConfig{SessionID: "browser-only"},
		Browser: types.BrowserConfig{
			Enabled:     true,
			Backend:     "cua",
			ProfileName: "profile_a",
		},
	}, nil, newTransportRuntimeFactory(t, fake, &factoryCalls, &requireBrowserCalls))
	if err != nil {
		t.Fatal(err)
	}
	if len(transports) != 4 {
		t.Fatalf("transport count = %d, want three built-ins plus browser", len(transports))
	}
	if factoryCalls != 1 {
		t.Fatalf("Cua runtime factory calls = %d, want 1", factoryCalls)
	}
	if !reflect.DeepEqual(requireBrowserCalls, []bool{true}) {
		t.Fatalf("browser requirements = %v, want [true]", requireBrowserCalls)
	}

	cancel()
	waitForTransportRuntimeClose(t, ended)
}

func TestStartTransportsCombinedCreatesOneSharedCUARuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fake, ended := startTransportFakeCUA(t)
	factoryCalls := 0
	requireBrowserCalls := []bool{}

	transports, err := startTransports(ctx, types.Config{
		WorkingDir: t.TempDir(),
		CUA:        types.CUAConfig{SessionID: "shared-run"},
		Computer:   types.ComputerConfig{Enabled: true, SessionID: "legacy-session"},
		Browser: types.BrowserConfig{
			Enabled:     true,
			Backend:     "cua",
			ProfileName: "profile_b",
		},
	}, nil, newTransportRuntimeFactory(t, fake, &factoryCalls, &requireBrowserCalls))
	if err != nil {
		t.Fatal(err)
	}
	if len(transports) != 5 {
		t.Fatalf("transport count = %d, want three built-ins plus computer and browser", len(transports))
	}
	if factoryCalls != 1 {
		t.Fatalf("Cua runtime factory calls = %d, want 1", factoryCalls)
	}
	if !reflect.DeepEqual(requireBrowserCalls, []bool{true}) {
		t.Fatalf("browser requirements = %v, want [true]", requireBrowserCalls)
	}
	calls := fake.Calls()
	if countFakeCUACalls(calls, "start_session") != 1 {
		t.Fatalf("start_session calls = %#v, want exactly one", calls)
	}
	if calls[0].Name != "start_session" || calls[0].Args["session"] != "shared-run" {
		t.Fatalf("first Cua call = %#v, want start_session for top-level configured session", calls[0])
	}

	cancel()
	waitForTransportRuntimeClose(t, ended)
	if calls := fake.Calls(); countFakeCUACalls(calls, "end_session") != 1 {
		t.Fatalf("end_session calls = %#v, want exactly one", calls)
	}
}

func TestStartTransportsChromedpBrowserCreatesNoCUARuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	factoryCalls := 0
	factoryErr := errors.New("Cua runtime factory must not be called")
	factory := func(context.Context, types.Config, bool) (*cuaRuntime, error) {
		factoryCalls++
		return nil, factoryErr
	}

	transports, err := startTransports(ctx, types.Config{
		WorkingDir: t.TempDir(),
		Browser:    types.BrowserConfig{Enabled: true, Backend: "chromedp"},
	}, nil, factory)
	if err != nil {
		t.Fatal(err)
	}
	if len(transports) != 4 {
		t.Fatalf("transport count = %d, want three built-ins plus browser", len(transports))
	}
	if factoryCalls != 0 {
		t.Fatalf("Cua runtime factory calls = %d, want 0", factoryCalls)
	}
}

func TestStartTransportsReturnsCUAConstructionErrorSynchronously(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wantErr := errors.New("Cua driver unavailable")
	factoryCalls := 0
	factory := func(context.Context, types.Config, bool) (*cuaRuntime, error) {
		factoryCalls++
		return nil, wantErr
	}

	transports, err := startTransports(ctx, types.Config{
		WorkingDir: t.TempDir(),
		Computer:   types.ComputerConfig{Enabled: true},
	}, nil, factory)
	if !errors.Is(err, wantErr) {
		t.Fatalf("startTransports error = %v, want %v", err, wantErr)
	}
	if transports != nil {
		t.Fatalf("transports = %#v, want nil on synchronous Cua construction failure", transports)
	}
	if factoryCalls != 1 {
		t.Fatalf("Cua runtime factory calls = %d, want 1", factoryCalls)
	}
}

func TestStartTransportsRejectsInvalidBrowserConfigBeforeStartingServers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	factoryCalls := 0
	factory := func(context.Context, types.Config, bool) (*cuaRuntime, error) {
		factoryCalls++
		return nil, errors.New("unexpected factory call")
	}

	transports, err := startTransports(ctx, types.Config{
		WorkingDir: t.TempDir(),
		Computer:   types.ComputerConfig{Enabled: true},
		Browser: types.BrowserConfig{
			Enabled:    true,
			Backend:    "cua",
			ProfileDir: t.TempDir(),
		},
	}, nil, factory)
	if err == nil || !strings.Contains(err.Error(), "profile_dir") {
		t.Fatalf("startTransports error = %v, want invalid Cua browser profile error", err)
	}
	if transports != nil {
		t.Fatalf("transports = %#v, want nil on invalid browser config", transports)
	}
	if factoryCalls != 0 {
		t.Fatalf("Cua runtime factory calls = %d, want 0 because validation must happen first", factoryCalls)
	}
}
