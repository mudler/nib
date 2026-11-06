package watch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSourceValidate(t *testing.T) {
	for name, s := range map[string]Source{"empty": {}, "both": {Script: "x", URL: "y"}} {
		if s.Validate() == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if err := (Source{Script: "true"}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRunScript(t *testing.T) {
	res := Run(context.Background(), Source{Script: "echo hello"})
	if res.Err != nil || res.Output != "hello\n" || res.Hash != Hash("hello\n") {
		t.Fatalf("got %+v", res)
	}
}

func TestRunScriptExitError(t *testing.T) {
	res := Run(context.Background(), Source{Script: "echo partial; exit 3"})
	var ee *ExitError
	if !errors.As(res.Err, &ee) || ee.Code != 3 || res.ExitCode != 3 {
		t.Fatalf("got %+v", res)
	}
	if res.Output != "partial\n" || res.Hash != "" {
		t.Fatalf("output %q hash %q", res.Output, res.Hash)
	}
}

func TestRunScriptTimeout(t *testing.T) {
	start := time.Now()
	res := Runner{Timeout: 200 * time.Millisecond}.Run(context.Background(), Source{Script: "sleep 30"})
	if res.Err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("got %+v after %s", res, time.Since(start))
	}
	var ee *ExitError
	if errors.As(res.Err, &ee) {
		t.Fatal("a timeout must not look like an exit status")
	}
}

func TestRunURL(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		fmt.Fprint(w, "body")
	}))
	defer srv.Close()
	res := Run(context.Background(), Source{URL: srv.URL})
	if res.Err != nil || res.Output != "body" {
		t.Fatalf("got %+v", res)
	}
	status = http.StatusBadGateway
	if res := Run(context.Background(), Source{URL: srv.URL}); res.Err == nil || res.Hash != "" {
		t.Fatalf("HTTP 502 must be an error, got %+v", res)
	}
}

func TestRunCapsOutput(t *testing.T) {
	res := Runner{MaxOutput: 10}.Run(context.Background(), Source{Script: "printf 'aaaaaaaaaaaaaaaaaaaaaaaa'"})
	if len(res.Output) != 10 || res.Hash != Hash(res.Output) {
		t.Fatalf("got %+v", res)
	}
}

func TestCapKeepsUTF8(t *testing.T) {
	got := Cap("aé", 2) // é is two bytes; the cut would split it
	if got != "a" {
		t.Fatalf("got %q", got)
	}
}

func TestCompare(t *testing.T) {
	ok := func(out string) Result { return Result{Output: out, Hash: Hash(out)} }

	base := Compare(Snapshot{}, ok("a"), 100)
	if base.Kind != Baseline || base.Snapshot.Hash != Hash("a") {
		t.Fatalf("first check: %+v", base)
	}
	same := Compare(base.Snapshot, ok("a"), 100)
	if same.Kind != Unchanged {
		t.Fatalf("same: %+v", same)
	}
	ch := Compare(base.Snapshot, ok("b"), 100)
	if ch.Kind != Changed || ch.Snapshot.Hash != Hash("b") || !strings.Contains(ch.Diff, "-a") || !strings.Contains(ch.Diff, "+b") {
		t.Fatalf("changed: %+v", ch)
	}
	fail := Compare(base.Snapshot, Result{Err: errors.New("boom")}, 100)
	if fail.Kind != Failed || fail.Snapshot != base.Snapshot || fail.Err == nil {
		t.Fatalf("an error must keep the stored snapshot: %+v", fail)
	}
	// An empty output is a real value, not "never checked".
	empty := Compare(Snapshot{}, ok(""), 100)
	if empty.Kind != Baseline || empty.Snapshot.Hash == "" {
		t.Fatalf("empty output: %+v", empty)
	}
}

func TestDiff(t *testing.T) {
	if Diff("a\nb\n", "a\nb\n", 0) != "" {
		t.Fatal("equal inputs must give an empty diff")
	}
	got := Diff("one\ntwo\nthree\nfour\nfive\n", "one\ntwo\nTHREE\nfour\nfive\nsix\n", 0)
	for _, want := range []string{"--- previous", "+++ current", "@@", "-three", "+THREE", "+six", " two"} {
		if !strings.Contains(got, want) {
			t.Errorf("diff lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, " one\n") {
		t.Errorf("context must be one line:\n%s", got)
	}
	// An inserted line must not mark everything after it as changed.
	ins := Diff("a\nb\nc\nd\n", "a\nX\nb\nc\nd\n", 0)
	if strings.Contains(ins, "-b") || strings.Contains(ins, "-c") || !strings.Contains(ins, "+X") {
		t.Errorf("insertion diff:\n%s", ins)
	}
}

func TestDiffCap(t *testing.T) {
	var old, new strings.Builder
	for i := range 500 {
		fmt.Fprintf(&old, "old line %d\n", i)
		fmt.Fprintf(&new, "new line %d\n", i)
	}
	got := Diff(old.String(), new.String(), 300)
	if len(got) > 300+len("... (truncated)\n") || !strings.HasSuffix(got, "... (truncated)\n") {
		t.Fatalf("len %d:\n%s", len(got), got)
	}
}

func TestDiffHugeChangeFallsBack(t *testing.T) {
	var old, new strings.Builder
	for i := range 3000 {
		fmt.Fprintf(&old, "a%d\n", i)
		fmt.Fprintf(&new, "b%d\n", i)
	}
	if got := Diff(old.String(), new.String(), 100); !strings.Contains(got, "-a0") {
		t.Fatalf("got %q", got)
	}
}

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func okRes(out string) Result { return Result{Output: out, Hash: Hash(out)} }

func newWaiter(t *testing.T, w Wait) *Waiter {
	t.Helper()
	if w.Source == (Source{}) {
		w.Source = Source{Script: "true"}
	}
	if w.Timeout == 0 {
		w.Timeout = time.Minute
	}
	wt, err := NewWaiter(w, t0)
	if err != nil {
		t.Fatal(err)
	}
	return wt
}

func TestWaiterChange(t *testing.T) {
	wt := newWaiter(t, Wait{Mode: UntilChange})
	if v := wt.Step(okRes("a"), t0.Add(time.Second)); v.Done {
		t.Fatalf("the first check is the baseline: %+v", v)
	}
	if v := wt.Step(okRes("a"), t0.Add(2*time.Second)); v.Done {
		t.Fatalf("unchanged: %+v", v)
	}
	v := wt.Step(okRes("b"), t0.Add(3*time.Second))
	if !v.Done || v.Reason != ReasonChanged || !strings.Contains(v.Detail, "+b") || !v.Reason.Satisfied() {
		t.Fatalf("got %+v", v)
	}
}

func TestWaiterMatch(t *testing.T) {
	wt := newWaiter(t, Wait{Mode: UntilMatch, Pattern: `status: (ok|done)`})
	if v := wt.Step(okRes("status: running"), t0); v.Done {
		t.Fatalf("got %+v", v)
	}
	v := wt.Step(okRes("x\nstatus: done\n"), t0.Add(time.Second))
	if !v.Done || v.Reason != ReasonMatched {
		t.Fatalf("got %+v", v)
	}
}

func TestWaiterExitZero(t *testing.T) {
	wt := newWaiter(t, Wait{Mode: UntilExitZero})
	// A non-zero exit means "not yet", and it does not count as a failing source.
	for i := range 10 {
		if v := wt.Step(Result{ExitCode: 1, Err: &ExitError{Code: 1}}, t0.Add(time.Duration(i)*time.Second)); v.Done {
			t.Fatalf("exit 1 ended the wait: %+v", v)
		}
	}
	v := wt.Step(Result{Output: "ready"}, t0.Add(20*time.Second))
	if !v.Done || v.Reason != ReasonExitedZero {
		t.Fatalf("got %+v", v)
	}
}

func TestWaiterRepeatedErrors(t *testing.T) {
	wt := newWaiter(t, Wait{Mode: UntilChange, MaxErrors: 3})
	boom := Result{Err: errors.New("boom")}
	for i := range 2 {
		if v := wt.Step(boom, t0.Add(time.Duration(i)*time.Second)); v.Done {
			t.Fatalf("ended after %d errors", i+1)
		}
	}
	// A good check resets the count.
	wt.Step(okRes("a"), t0.Add(3*time.Second))
	for i := range 2 {
		if v := wt.Step(boom, t0.Add(time.Duration(4+i)*time.Second)); v.Done {
			t.Fatalf("the count was not reset: %+v", v)
		}
	}
	v := wt.Step(boom, t0.Add(10*time.Second))
	if !v.Done || v.Reason != ReasonSourceFailing || v.Reason.Satisfied() || !strings.Contains(v.Detail, "boom") {
		t.Fatalf("got %+v", v)
	}
}

func TestWaiterExitZeroRealErrorsStillCount(t *testing.T) {
	wt := newWaiter(t, Wait{Mode: UntilExitZero, MaxErrors: 2})
	e := Result{Err: errors.New("cannot start")}
	wt.Step(e, t0)
	if v := wt.Step(e, t0.Add(time.Second)); v.Reason != ReasonSourceFailing {
		t.Fatalf("got %+v", v)
	}
}

func TestWaiterTimeout(t *testing.T) {
	wt := newWaiter(t, Wait{Mode: UntilMatch, Pattern: "never", Timeout: 10 * time.Second})
	if v := wt.Step(okRes("a"), t0.Add(9*time.Second)); v.Done {
		t.Fatalf("early: %+v", v)
	}
	v := wt.Step(okRes("still waiting"), t0.Add(10*time.Second))
	if !v.Done || v.Reason != ReasonTimedOut || v.Reason.Satisfied() || !strings.Contains(v.Detail, "still waiting") {
		t.Fatalf("got %+v", v)
	}
	// A timeout with a failing source names the error.
	wt = newWaiter(t, Wait{Mode: UntilChange, Timeout: 10 * time.Second, MaxErrors: 99})
	v = wt.Step(Result{Err: errors.New("down")}, t0.Add(11*time.Second))
	if v.Reason != ReasonTimedOut || !strings.Contains(v.Detail, "down") {
		t.Fatalf("got %+v", v)
	}
}

func TestWaiterConditionBeatsDeadline(t *testing.T) {
	wt := newWaiter(t, Wait{Mode: UntilMatch, Pattern: "ok", Timeout: time.Second})
	if v := wt.Step(okRes("ok"), t0.Add(time.Hour)); v.Reason != ReasonMatched {
		t.Fatalf("got %+v", v)
	}
}

func TestNewWaiterValidation(t *testing.T) {
	bad := map[string]Wait{
		"no source":    {Timeout: time.Second},
		"no timeout":   {Source: Source{Script: "true"}},
		"bad regex":    {Source: Source{Script: "true"}, Mode: UntilMatch, Pattern: "(", Timeout: time.Second},
		"exit on url":  {Source: Source{URL: "http://x"}, Mode: UntilExitZero, Timeout: time.Second},
		"unknown mode": {Source: Source{Script: "true"}, Mode: 9, Timeout: time.Second},
	}
	for name, w := range bad {
		if _, err := NewWaiter(w, t0); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func ExampleCompare() {
	prev := Snapshot{}
	for _, out := range []string{"v1\n", "v1\n", "v2\n"} {
		res := Result{Output: out, Hash: Hash(out)}
		o := Compare(prev, res, 1000)
		fmt.Println(o.Kind)
		prev = o.Snapshot
	}
	// Output:
	// baseline
	// unchanged
	// changed
}
