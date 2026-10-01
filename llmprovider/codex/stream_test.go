package codex

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mudler/cogito"
	openai "github.com/sashabaranov/go-openai"
)

type streamTransport struct{ target *url.URL }

func (s streamTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme = s.target.Scheme
	r.URL.Host = s.target.Host
	return http.DefaultTransport.RoundTrip(r)
}
func streamClient(t *testing.T, handler http.HandlerFunc) *LLM {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	l := New(Config{Model: "gpt-5", Token: "test-token"})
	l.client = &http.Client{Transport: streamTransport{u}}
	return l
}
func startStream(t *testing.T, ctx context.Context, l *LLM) <-chan cogito.StreamEvent {
	t.Helper()
	s, ok := any(l).(cogito.StreamingLLM)
	if !ok {
		t.Fatal("Codex does not implement cogito.StreamingLLM")
	}
	ch, err := s.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{Model: "gpt-5"})
	if err != nil {
		t.Fatal(err)
	}
	return ch
}
func nextStream(t *testing.T, ch <-chan cogito.StreamEvent) cogito.StreamEvent {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("stream closed early")
		}
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("stream blocked before terminal completion")
		return cogito.StreamEvent{}
	}
}
func TestStreamReasoningBeforeCompletion(t *testing.T) {
	release := make(chan struct{})

	l := streamClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing auth")
		}
		w.Header().Set(headerTurnState, "stream-state")
		fmt.Fprint(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"output_index\":0,\"summary_index\":0,\"delta\":\"Checking.\"}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"Checking."}]},{"type":"message","content":[{"type":"output_text","text":"Done"}]}]}}`+"\n\n")
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := startStream(t, ctx, l)
	ev := nextStream(t, ch)
	if ev.Type != cogito.StreamEventReasoning || ev.Content != "Checking." {
		t.Fatalf("early event: %+v", ev)
	}
	if l.session.turnState != "stream-state" {
		t.Fatal("lost session state")
	}
	close(release)
	if ev := nextStream(t, ch); ev.Type != cogito.StreamEventContent || ev.Content != "Done" {
		t.Fatalf("%+v", ev)
	}
	if ev := nextStream(t, ch); ev.Type != cogito.StreamEventDone {
		t.Fatalf("%+v", ev)
	}
	if _, ok := <-ch; ok {
		t.Fatal("extra final output")
	}
}
func TestStreamMixedAndFallback(t *testing.T) {
	for _, prefix := range []string{"", `data: {"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"I checked "}

data: {"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"the available operations."}

data: {"type":"response.output_text.delta","output_index":1,"content_index":0,"delta":"Hi!"}

data: {"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","call_id":"call_1","name":"bash","arguments":""}}

data: {"type":"response.function_call_arguments.delta","output_index":2,"delta":"{\"command\":\"ls\"}"}

`} {
		t.Run(fmt.Sprint(len(prefix)), func(t *testing.T) {
			l := streamClient(t, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, prefix+`data: {"type":"response.reasoning_text.delta","delta":"SECRET"}

data: {"type":"response.reasoning.delta","delta":"SECRET"}

`+codexSSE)
			})
			ch := startStream(t, context.Background(), l)
			var reasoning, content, args, name, id string
			done := 0
			for ev := range ch {
				switch ev.Type {
				case cogito.StreamEventReasoning:
					reasoning += ev.Content
				case cogito.StreamEventContent:
					content += ev.Content
				case cogito.StreamEventToolCall:
					args += ev.ToolArgs
					if ev.ToolName != "" {
						name = ev.ToolName
					}
					if ev.ToolCallID != "" {
						id = ev.ToolCallID
					}
				case cogito.StreamEventError:
					t.Fatal(ev.Error)
				case cogito.StreamEventDone:
					done++
					if ev.Usage.TotalTokens != 13 || ev.FinishReason != "tool_calls" {
						t.Fatalf("done: %+v", ev)
					}
				}
			}
			if reasoning != "I checked the available operations." || content != "Hi!" || args != `{"command":"ls"}` || name != "bash" || id != "call_1" || done != 1 {
				t.Fatalf("reasoning=%q content=%q args=%q name=%q id=%q done=%d", reasoning, content, args, name, id, done)
			}
		})
	}
}
func TestStreamTerminalOnlySummary(t *testing.T) {
	l := streamClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"reasoning","encrypted_content":"SECRET","content":[{"type":"reasoning_text","text":"SECRET"}],"summary":[{"type":"summary_text","text":"Public"}]},{"type":"message","content":[{"type":"output_text","text":"Answer"}]}]}}

`)
	})
	ch := startStream(t, context.Background(), l)
	if ev := nextStream(t, ch); ev.Type != cogito.StreamEventReasoning || ev.Content != "Public" {
		t.Fatalf("%+v", ev)
	}
	if ev := nextStream(t, ch); ev.Type != cogito.StreamEventContent || ev.Content != "Answer" {
		t.Fatalf("%+v", ev)
	}
	if ev := nextStream(t, ch); ev.Type != cogito.StreamEventDone {
		t.Fatalf("%+v", ev)
	}
	if _, ok := <-ch; ok {
		t.Fatal("extra terminal output")
	}
}
func TestStreamErrors(t *testing.T) {
	for _, body := range []string{`data: {"type":"error","message":"broken"}` + "\n\n", `data: {"type":"response.failed","response":{"error":{"code":"bad","message":"broken"}}}` + "\n\n", "data: {}\n\n", "data: invalid\n\n"} {
		l := streamClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
		ch := startStream(t, context.Background(), l)
		ev := nextStream(t, ch)
		if ev.Type != cogito.StreamEventError || ev.Error == nil {
			t.Fatalf("%+v", ev)
		}
		if _, ok := <-ch; ok {
			t.Fatal("extra event after error")
		}
	}
}
func TestStreamCancellation(t *testing.T) {
	l := streamClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, ": heartbeat\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := startStream(t, ctx, l)
	cancel()
	select {
	case ev, ok := <-ch:
		if ok && (ev.Type != cogito.StreamEventError || !errors.Is(ev.Error, context.Canceled)) {
			t.Fatalf("%+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not close stream")
	}
}
func TestStreamHTTPError(t *testing.T) {
	l := streamClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":{"message":"unauthorized"}}`)
	})
	s, ok := any(l).(cogito.StreamingLLM)
	if !ok {
		t.Fatal("missing StreamingLLM")
	}
	_, err := s.CreateChatCompletionStream(context.Background(), openai.ChatCompletionRequest{})
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("%v", err)
	}
}

func TestStreamSparseFallbackAndPartialDeltas(t *testing.T) {
	l := streamClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `: keepalive

data:{"type":"response.reasoning_summary_text.delta","output_index":4,"summary_index":0,"delta":"First"}

data: {"type":"response.output_text.delta","output_index":7,"content_index":0,"delta":"An"}

data: {"type":"response.output_item.done","output_index":7,"item":{"type":"message","content":[{"type":"output_text","text":"Answer"}]}}

data: {"type":"response.output_item.done","output_index":4,"item":{"type":"reasoning","summary":[{"type":"summary_text","text":"First summary"},{"type":"summary_text","text":"Second"}]}}

data: {"type":"response.completed",
data: "response":{"status":"completed","output":[]}}

`)
	})
	var content, reasoning string
	done := 0
	for ev := range startStream(t, context.Background(), l) {
		switch ev.Type {
		case cogito.StreamEventContent:
			content += ev.Content
		case cogito.StreamEventReasoning:
			reasoning += ev.Content
		case cogito.StreamEventDone:
			done++
		case cogito.StreamEventError:
			t.Fatal(ev.Error)
		}
	}
	if content != "Answer" || reasoning != "First summary\nSecond" || done != 1 {
		t.Fatalf("%q %q %d", content, reasoning, done)
	}
}

func TestStreamCancellationWithBackpressure(t *testing.T) {
	l := streamClient(t, func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 200; i++ {
			fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n")
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := startStream(t, ctx, l)
	// Leave more events than the buffer can hold unread, then cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()
	closed := make(chan struct{})
	go func() {
		for range ch {
		}
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("blocked producer after cancel")
	}
}
