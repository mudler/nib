package codeindex

import (
	"context"
	"encoding/binary"
	"errors"
	"github.com/msuozzo/bonsai"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

type hangingExtractor struct{}

func (hangingExtractor) Extract(*bonsai.Node, []byte) []Entry {
	for {
		time.Sleep(time.Hour)
	}
}

func TestWorkerHelper(t *testing.T) {
	mode := os.Getenv("NIB_TEST_WORKER")
	if mode == "" {
		return
	}
	if mode == "startup-hang" {
		for {
			time.Sleep(time.Hour)
		}
	}
	if mode == "extraction-hang" {
		byExt[".go"].extractor = hangingExtractor{}
	}
	if mode == "real" || mode == "extraction-hang" {
		if err := ServeWorker(os.Stdin, os.Stdout); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	_ = writeFrame(os.Stdout, []byte(`{"ready":true}`), maxWorkerResponse)
	_, _ = readFrame(os.Stdin, maxWorkerRequest)
	switch mode {
	case "hang":
		for {
			time.Sleep(time.Hour)
		}
	case "crash":
		os.Exit(3)
	case "malformed":
		_ = writeFrame(os.Stdout, []byte(`{`), maxWorkerResponse)
	case "oversize":
		_ = binary.Write(os.Stdout, binary.BigEndian, uint32(maxWorkerResponse+1))
	}
	os.Exit(0)
}

func testWorker(t *testing.T, ctx context.Context, mode string) *Worker {
	t.Helper()
	w := NewWorker(ctx)
	w.command = func() *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestWorkerHelper$")
		cmd.Env = append(os.Environ(), "NIB_TEST_WORKER="+mode)
		return cmd
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func assertReaped(t *testing.T, w *Worker) {
	t.Helper()
	if w.cmd == nil || w.cmd.ProcessState == nil {
		t.Fatal("worker was not started and reaped")
	}
	if _, _, err := w.ParseSource(context.Background(), "x.go", nil); !errors.Is(err, ErrWorkerClosed) {
		t.Fatalf("worker reused after failure: %v", err)
	}
}

func TestWorkerTimeout(t *testing.T) {
	for _, mode := range []string{"startup-hang", "hang", "extraction-hang"} {
		t.Run(mode, func(t *testing.T) {
			w := testWorker(t, context.Background(), mode)
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, _, err := w.ParseSource(ctx, "x.go", []byte("package x"))
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("got %v", err)
			}
			if time.Since(start) > 3*time.Second {
				t.Fatal("cancellation did not stop worker promptly")
			}
			assertReaped(t, w)
		})
	}
}

func TestWorkerCancel(t *testing.T) {
	for _, overall := range []bool{false, true} {
		t.Run(map[bool]string{false: "request", true: "overall"}[overall], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			life, req := context.Background(), ctx
			if overall {
				life, req = ctx, context.Background()
			}
			w := testWorker(t, life, "hang")
			time.AfterFunc(150*time.Millisecond, cancel)
			_, _, err := w.ParseSource(req, "x.go", nil)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v", err)
			}
			assertReaped(t, w)
		})
	}
}

func TestWorkerBadResponse(t *testing.T) {
	for _, mode := range []string{"crash", "malformed", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			w := testWorker(t, context.Background(), mode)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, _, err := w.ParseSource(ctx, "x.go", nil); err == nil {
				t.Fatal("expected failure")
			}
			assertReaped(t, w)
		})
	}
}

func TestWorkerReal(t *testing.T) {
	w := testWorker(t, context.Background(), "real")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, file := range []struct{ name, source string }{{"x.go", "package x\nfunc Hello() {}\n"}, {"Dockerfile", "FROM alpine\nRUN echo hi\n"}, {"x.py", "def hello():\n    pass\n"}} {
		path := writeTempFile(t, file.name, file.source)
		want, wantError, err := Entries(path)
		if err != nil {
			t.Fatal(err)
		}
		got, hasError, err := w.ParseSource(ctx, file.name, []byte(file.source))
		if err != nil || hasError != wantError || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s got %v %v %v; want %v", file.name, got, hasError, err, want)
		}
	}
	cmd := w.cmd
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if cmd.ProcessState == nil {
		t.Fatal("Close did not reap")
	}
	_ = w.Close()
}

func TestWorkerSourceLimit(t *testing.T) {
	w := testWorker(t, context.Background(), "real")
	if _, _, err := w.ParseSource(context.Background(), "x.go", []byte(strings.Repeat("x", WorkerMaxSourceBytes+1))); err == nil {
		t.Fatal("accepted oversized source")
	}
	if w.cmd != nil {
		t.Fatal("started worker for oversized input")
	}
}

func TestWorkerDefaultTimeout(t *testing.T) {
	w := testWorker(t, context.Background(), "hang")
	start := time.Now()
	_, _, err := w.ParseSource(context.Background(), "x.go", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("default timeout not enforced")
	}
	assertReaped(t, w)
}

func TestWorkerCloseActive(t *testing.T) {
	w := testWorker(t, context.Background(), "hang")
	timer := time.AfterFunc(150*time.Millisecond, func() { _ = w.Close() })
	defer timer.Stop()
	_, _, err := w.ParseSource(context.Background(), "x.go", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	assertReaped(t, w)
}

func TestWorkerQueuedCancellation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "deadline"}[deadline], func(t *testing.T) {
			w := testWorker(t, context.Background(), "hang")
			started := make(chan struct{})
			command := w.command
			w.command = func() *exec.Cmd {
				close(started) // The active request owns serialized admission.
				return command()
			}
			activeCtx, stopActive := context.WithCancel(context.Background())
			activeDone := make(chan error, 1)
			go func() {
				_, _, err := w.ParseSource(activeCtx, "active.go", nil)
				activeDone <- err
			}()
			defer func() {
				stopActive()
				select {
				case <-activeDone:
				case <-time.After(3 * time.Second):
					t.Error("active request did not stop")
				}
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("active request did not start")
			}

			ctx, cancel := context.WithCancel(context.Background())
			want := context.Canceled
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
				want = context.DeadlineExceeded
			} else {
				timer := time.AfterFunc(20*time.Millisecond, cancel)
				defer timer.Stop()
			}
			defer cancel()
			queuedDone := make(chan error, 1)
			go func() {
				_, _, err := w.ParseSource(ctx, "queued.go", nil)
				queuedDone <- err
			}()
			select {
			case err := <-queuedDone:
				if !errors.Is(err, want) {
					t.Errorf("queued request got %v, want %v", err, want)
				}
			case <-time.After(300 * time.Millisecond):
				t.Error("queued request ignored cancellation while active request held admission")
				stopActive()
				select {
				case <-queuedDone:
				case <-time.After(3 * time.Second):
					t.Fatal("queued request did not stop")
				}
				return
			}
			select {
			case err := <-activeDone:
				activeDone <- err // Leave cleanup to join the active request.
				t.Fatalf("queued cancellation interrupted active request: %v", err)
			default:
			}
			if err := w.ctx.Err(); err != nil {
				t.Fatalf("queued cancellation disposed worker: %v", err)
			}
		})
	}
}
