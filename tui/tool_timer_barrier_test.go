package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/mudler/cogito"
	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/codexapp"
	codexhttp "github.com/mudler/nib/llmprovider/codex"
	openai "github.com/sashabaranov/go-openai"
)

type timerTool struct {
	name string
	run  func()
}

func (x timerTool) Tool() openai.Tool {
	return openai.Tool{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: x.name, Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}}
}
func (x timerTool) Execute(map[string]any) (string, any, error) {
	x.run()
	return "FAST_RESULT", nil, nil
}

type timerTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (x timerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme = x.target.Scheme
	r.URL.Host = x.target.Host
	return x.base.RoundTrip(r)
}
func timerWait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("barrier not reached")
	}
}

// These tests bridge Cogito's real execution callbacks to the production UI
// mailbox. Session permission/hooks and the Bubble Tea command scheduler are
// deliberately outside this bounded diagnostic.
func TestTimerBarrierCompletedBeforeNextReasoning(t *testing.T) {
	for _, provider := range []string{"http", "app-server"} {
		t.Run(provider, func(t *testing.T) { timerScenario(t, provider, false) })
	}
}
func TestTimerBarrierSlowSibling(t *testing.T) { timerScenario(t, "http", true) }

func timerScenario(t *testing.T, provider string, sibling bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	next := make(chan struct{}, 1)
	reasoning := make(chan struct{}, 1)
	fast := make(chan struct{})
	slow := make(chan struct{})
	slowRelease := make(chan struct{})
	var slowOnce sync.Once
	unblockSlow := func() { slowOnce.Do(func() { close(slowRelease) }) }
	defer unblockSlow()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if provider == "app-server" {
			next <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		if calls == 1 {
			items := []map[string]any{{"type": "function_call", "call_id": "fast-id", "name": "fast", "arguments": "{}"}}
			if sibling {
				items = append(items, map[string]any{"type": "function_call", "call_id": "slow-id", "name": "slow", "arguments": "{}"})
			}
			for i, item := range items {
				b, _ := json.Marshal(map[string]any{"type": "response.output_item.added", "output_index": i, "item": item})
				fmt.Fprintf(w, "data: %s\n\n", b)
			}
			b, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": items}})
			fmt.Fprintf(w, "data: %s\n\n", b)
			return
		}
		next <- struct{}{}
		fmt.Fprint(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"output_index\":0,\"summary_index\":0,\"delta\":\"Checking.\"}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"Done\"}]}]}}\n\n")
	}))
	defer srv.Close()
	defer unblock()
	var llm cogito.LLM
	if provider == "http" {
		target, _ := url.Parse(srv.URL)
		old := http.DefaultTransport
		http.DefaultTransport = timerTransport{target, old}
		defer func() { http.DefaultTransport = old }()
		llm = codexhttp.New(codexhttp.Config{Model: "gpt-5", Token: "synthetic-test-token"})
	} else {
		llm = codexapp.New(codexapp.Config{Command: os.Args[0], Args: []string{"-test.run=^TestTimerBarrierAppHelper$", "--", srv.URL}})
	}
	m := runningTestModel()
	m.reasoningChan = make(chan reasoningEvent, 32)
	m.toolEvents = newToolEventQueue()
	m.toolEvents.begin()
	start, result := m.toolCallbacks()
	results := make(chan string, 4)
	starts := make(chan string, 4)
	reasoningChan := m.reasoningChan
	tools := []cogito.ToolDefinitionInterface{timerTool{"fast", func() { close(fast) }}}
	if sibling {
		tools = append(tools, timerTool{"slow", func() {
			close(slow)
			select {
			case <-slowRelease:
			case <-ctx.Done():
			}
		}})
	}
	done := make(chan error, 1)
	go func() {
		_, err := cogito.ExecuteTools(llm, cogito.NewEmptyFragment().AddMessage(cogito.UserMessageRole, "Run the supplied tools."),
			cogito.WithContext(ctx), cogito.WithTools(tools...), cogito.WithIterations(3), cogito.DisableSinkState, cogito.EnableParallelToolExecution,
			cogito.WithToolCallBack(func(tc *cogito.ToolChoice, _ *cogito.SessionState) cogito.ToolCallDecision {
				b, _ := json.Marshal(tc.Arguments)
				start(chat.ToolStart{ID: tc.ID, Name: tc.Name, Arguments: string(b)})
				starts <- tc.ID
				return cogito.ToolCallDecision{Approved: true}
			}),
			cogito.WithToolCallResultCallback(func(st cogito.ToolStatus) {
				b, _ := json.Marshal(st.ToolArguments.Arguments)
				result(chat.ToolResult{ID: st.ToolArguments.ID, Name: st.Name, Arguments: string(b), Result: st.Result})
				results <- st.ToolArguments.ID
			}),
			cogito.WithStreamCallback(func(ev cogito.StreamEvent) {
				if ev.Type == cogito.StreamEventReasoning {
					reasoningChan <- reasoningEvent{kind: reasoningEventDelta, text: ev.Content}
					reasoning <- struct{}{}
				}
			}),
		)
		done <- err
	}()
	timerWait(t, fast)
	if sibling {
		timerWait(t, slow)
		select {
		case id := <-results:
			t.Fatalf("unexpected early batch callback %s", id)
		case <-time.After(100 * time.Millisecond):
		}
		m = update(m, toolEventsReadyMsg{})
		if len(m.running) != 2 {
			t.Fatalf("held sibling: running=%d, want 2", len(m.running))
		}
		t.Log("fast execution reached return, but both UI entries remain running while sibling is held")
		unblockSlow()
	}
	timerWait(t, next)
	if provider == "http" {
		timerWait(t, reasoning)
	}
	want := 1
	if sibling {
		want = 2
	}
	expected := map[string]bool{}
	for i := 0; i < want; i++ {
		id := <-starts
		if id == "" {
			t.Fatal("empty start ID")
		}
		expected[id] = true
	}
	for i := 0; i < want; i++ {
		select {
		case id := <-results:
			if !expected[id] {
				t.Fatalf("result did not match start ID %q", id)
			}
		case <-ctx.Done():
			t.Fatal("missing result")
		}
	}
	m = update(m, toolEventsReadyMsg{})
	m = update(m, m.listenReasoningEvents()())
	if len(m.running) != 0 || !m.loading {
		t.Fatalf("while next request held: running=%d loading=%v", len(m.running), m.loading)
	}
	meta := []string{}
	for _, msg := range m.messages {
		if msg.Role == "tool" {
			meta = append(meta, msg.Meta)
		}
	}
	if len(meta) != want {
		t.Fatalf("completed entries=%d", len(meta))
	}
	for i := 0; i < 3; i++ {
		m = update(m, spinner.TickMsg{})
	}
	j := 0
	for _, msg := range m.messages {
		if msg.Role == "tool" {
			if msg.Meta != meta[j] {
				t.Fatal("completed duration changed")
			}
			j++
		}
	}
	select {
	case err := <-done:
		t.Fatalf("turn completed before barrier release: %v", err)
	default:
	}
	t.Log("result IDs delivered and running entries removed before next request completes")
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("turn failed to complete")
	}
}

// Isolated fake app-server: tool request first, then a held second turn. This
// adapter does not stream reasoning; the local barrier proves the next request is active.
func TestTimerBarrierAppHelper(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--" {
		return
	}
	endpoint := os.Args[len(os.Args)-1]
	scan := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scan.Scan() {
		var req struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scan.Bytes(), &req) != nil {
			os.Exit(2)
		}
		switch req.Method {
		case "initialize":
			enc.Encode(map[string]any{"id": req.ID, "result": map[string]any{}})
		case "thread/start":
			enc.Encode(map[string]any{"id": req.ID, "result": map[string]any{"thread": map[string]any{"id": "thread"}}})
		case "turn/start":
			enc.Encode(map[string]any{"id": req.ID, "result": map[string]any{"turn": map[string]any{"id": "turn"}}})
			if !strings.Contains(string(req.Params), "FAST_RESULT") {
				enc.Encode(map[string]any{"id": "request", "method": "item/tool/call", "params": map[string]any{"callId": "fast-id", "tool": "fast", "arguments": map[string]any{}}})
				continue
			}
			client := http.Client{Timeout: 8 * time.Second}
			resp, err := client.Get(endpoint)
			if err != nil {
				os.Exit(3)
			}
			resp.Body.Close()
			enc.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"item": map[string]any{"type": "agentMessage", "text": "Done"}}})
			enc.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]any{"id": "turn", "status": "completed"}}})
		}
	}
	os.Exit(0)
}
