package chat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mudler/cogito"
)

type fakeAgents struct {
	mu     sync.Mutex
	agents map[string]*cogito.AgentState
}

func newFakeAgents(states map[string]cogito.AgentStatusType) *fakeAgents {
	f := &fakeAgents{agents: map[string]*cogito.AgentState{}}
	for id, st := range states {
		f.agents[id] = &cogito.AgentState{ID: id, Task: "task of " + id, Status: st}
	}
	return f
}

func (f *fakeAgents) set(id string, st cogito.AgentStatusType, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agents[id].Status, f.agents[id].Error = st, err
}

func (f *fakeAgents) Get(id string) (*cogito.AgentState, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.agents[id]
	if !ok {
		return nil, false
	}
	c := *a
	return &c, true
}

func (f *fakeAgents) List() []*cogito.AgentState {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*cogito.AgentState
	for _, a := range f.agents {
		c := *a
		out = append(out, &c)
	}
	return out
}

func fastPoll(t *testing.T) {
	old := waitAgentsPoll
	waitAgentsPoll = 5 * time.Millisecond
	t.Cleanup(func() { waitAgentsPoll = old })
}

func TestWaitAgentsAllReturnsWhenEveryAgentIsDone(t *testing.T) {
	fastPoll(t)
	f := newFakeAgents(map[string]cogito.AgentStatusType{"a": cogito.AgentStatusRunning, "b": cogito.AgentStatusRunning})
	go func() {
		time.Sleep(30 * time.Millisecond)
		f.set("a", cogito.AgentStatusCompleted, nil)
		time.Sleep(30 * time.Millisecond)
		f.set("b", cogito.AgentStatusCompleted, nil)
	}()
	start := time.Now()
	out := waitAgents(context.Background(), f, []string{"a", "b"}, waitModeAll, 5*time.Second)
	if !strings.Contains(out, "All agents are done") || !strings.Contains(out, "- a: completed") || !strings.Contains(out, "- b: completed") {
		t.Fatalf("got:\n%s", out)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("did not return as soon as the last agent finished")
	}
}

func TestWaitAgentsAllKeepsWaitingAfterAFailure(t *testing.T) {
	fastPoll(t)
	f := newFakeAgents(map[string]cogito.AgentStatusType{"a": cogito.AgentStatusFailed, "b": cogito.AgentStatusRunning})
	out := waitAgents(context.Background(), f, []string{"a", "b"}, waitModeAll, 100*time.Millisecond)
	if !strings.Contains(out, "Timed out") {
		t.Fatalf("mode all returned before b finished:\n%s", out)
	}
}

func TestWaitAgentsFailsEarly(t *testing.T) {
	fastPoll(t)
	f := newFakeAgents(map[string]cogito.AgentStatusType{"a": cogito.AgentStatusRunning, "b": cogito.AgentStatusRunning})
	go func() {
		time.Sleep(30 * time.Millisecond)
		f.set("a", cogito.AgentStatusFailed, errors.New("tool crashed"))
	}()
	start := time.Now()
	out := waitAgents(context.Background(), f, []string{"a", "b"}, waitModeAnyFailedOrAll, 5*time.Second)
	for _, want := range []string{"failed", "Stopped waiting early", "- a: failed", "tool crashed", "- b: running"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("did not stop at the failure")
	}
}

func TestWaitAgentsTimeoutReturnsStatuses(t *testing.T) {
	fastPoll(t)
	f := newFakeAgents(map[string]cogito.AgentStatusType{"a": cogito.AgentStatusRunning, "b": cogito.AgentStatusCompleted})
	out := waitAgents(context.Background(), f, []string{"a", "b"}, waitModeAll, 50*time.Millisecond)
	for _, want := range []string{"Timed out", "again", "- a: running", "- b: completed"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

func TestWaitAgentsAlreadyDoneReturnsAtOnce(t *testing.T) {
	f := newFakeAgents(map[string]cogito.AgentStatusType{"a": cogito.AgentStatusCompleted})
	start := time.Now()
	out := waitAgents(context.Background(), f, []string{"a"}, waitModeAll, time.Minute)
	if !strings.Contains(out, "All agents are done") || time.Since(start) > time.Second {
		t.Fatalf("got %q after %s", out, time.Since(start))
	}
}

func TestWaitAgentsUnknownIDAndDefaultIDs(t *testing.T) {
	f := newFakeAgents(map[string]cogito.AgentStatusType{"a": cogito.AgentStatusCompleted})
	if out := waitAgents(context.Background(), f, []string{"a", "zz"}, waitModeAll, time.Second); !strings.Contains(out, "Unknown sub-agent id(s): zz") {
		t.Fatalf("got %q", out)
	}
	if out := waitAgents(context.Background(), f, nil, waitModeAll, time.Second); out != "No sub-agent is running." {
		t.Fatalf("got %q", out)
	}
}

func TestWaitAgentsCancelledByContext(t *testing.T) {
	fastPoll(t)
	f := newFakeAgents(map[string]cogito.AgentStatusType{"a": cogito.AgentStatusRunning})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	out := waitAgents(ctx, f, []string{"a"}, waitModeAll, time.Minute)
	if !strings.Contains(out, "cancelled") || !strings.Contains(out, "- a: running") {
		t.Fatalf("got %q", out)
	}
}

func TestWaitAgentsToolClampsTimeoutAndChecksMode(t *testing.T) {
	f := newFakeAgents(map[string]cogito.AgentStatusType{"a": cogito.AgentStatusCompleted})
	tool := &waitAgentsTool{src: f, ctx: context.Background()}
	if out, _, _ := tool.Run(map[string]any{"ids": []any{"a"}, "mode": "bogus"}); !strings.Contains(out, "Unknown mode") {
		t.Fatalf("got %q", out)
	}
	out, _, _ := tool.Run(map[string]any{"ids": []any{"a"}, "timeout_seconds": float64(1)})
	if !strings.Contains(out, "All agents are done") {
		t.Fatalf("got %q", out)
	}
	if got := clampSeconds(0, waitAgentsDefaultTimeout, waitAgentsMinTimeout, waitAgentsMaxTimeout); got != 30*time.Second {
		t.Fatalf("default %s", got)
	}
	if got := clampSeconds(1, waitAgentsDefaultTimeout, waitAgentsMinTimeout, waitAgentsMaxTimeout); got != 10*time.Second {
		t.Fatalf("min %s", got)
	}
	if got := clampSeconds(99999, waitAgentsDefaultTimeout, waitAgentsMinTimeout, waitAgentsMaxTimeout); got != 10*time.Minute {
		t.Fatalf("max %s", got)
	}
}

func TestWaitAgentsReadsTheRealManager(t *testing.T) {
	m := cogito.NewAgentManager()
	m.Register(&cogito.AgentState{ID: "x", Task: "t", Status: cogito.AgentStatusCompleted})
	if out := waitAgents(context.Background(), m, []string{"x"}, waitModeAll, time.Second); !strings.Contains(out, "- x: completed") {
		t.Fatalf("got %q", out)
	}
}

func TestWaitForToolValidatesAndClamps(t *testing.T) {
	var got WaitRequest
	tool := &waitForTool{arm: func(r WaitRequest) string { got = r; return "armed" }}
	for name, args := range map[string]map[string]any{
		"none":      {},
		"both":      {"script": "x", "url": "y"},
		"bad until": {"script": "x", "until": "sometime"},
	} {
		got = WaitRequest{}
		if out, _, _ := tool.Run(args); out == "armed" || got.Script != "" {
			t.Errorf("%s: armed anyway (%q)", name, out)
		}
	}
	out, _, _ := tool.Run(map[string]any{"script": "x", "timeout_seconds": float64(1), "interval_seconds": float64(1)})
	if out != "armed" || got.Timeout != waitForMinTimeout || got.IntervalSeconds != waitForMinInterval {
		t.Fatalf("got %q %+v", out, got)
	}
	tool.Run(map[string]any{"url": "u"})
	if got.Timeout != waitForDefaultTimeout || got.IntervalSeconds != waitForDefaultInterval {
		t.Fatalf("defaults: %+v", got)
	}
	tool.Run(map[string]any{"url": "u", "timeout_seconds": float64(10 * 24 * 3600)})
	if got.Timeout != waitForMaxTimeout {
		t.Fatalf("max: %+v", got)
	}
}

func TestWaitToolsNeedNoApprovalWhenSafe(t *testing.T) {
	cmds := newReadOnlyCommands(nil)
	if !IsReadOnly("wait_agents", `{}`, cmds) {
		t.Error("wait_agents observes only")
	}
	if !IsReadOnly("wait_for", `{"url":"https://example.com/x"}`, cmds) {
		t.Error("a URL check is a GET")
	}
	if !IsReadOnly("wait_for", `{"script":"ls /tmp"}`, cmds) {
		t.Error("a read-only command needs no approval")
	}
	if IsReadOnly("wait_for", `{"script":"rm -rf x"}`, cmds) {
		t.Error("an arbitrary command must be approved like bash")
	}
	if IsReadOnly("wait_for", `{"script":"ls","url":"u"}`, cmds) || IsReadOnly("wait_for", `{}`, cmds) {
		t.Error("an invalid call must not be auto-approved")
	}
}
