package codeindex

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

const (
	// InternalWorkerArg is a private executable mode, not a user command.
	InternalWorkerArg    = "--internal-codeindex-worker"
	WorkerMaxSourceBytes = 512 << 10
	WorkerParseTimeout   = time.Second
	maxWorkerRequest     = 1 << 20 // JSON includes base64 source and a bounded filename.
	maxWorkerResponse    = 4 << 20
	maxWorkerFilename    = 4096
)

var ErrWorkerClosed = errors.New("codeindex worker closed")

type workerRequest struct {
	Filename string
	Source   []byte
}
type workerResponse struct {
	Ready    bool
	Entries  []Entry
	HasError bool
	Error    string
}

// Worker isolates both parsing and extraction in one disposable subprocess.
// ParseSource calls are serialized. Any admitted request failure permanently
// closes the worker; cancellation while queued leaves it untouched. Callers must
// not retry indefinitely. Close kills and reaps the child.
// The executable must dispatch InternalWorkerArg to ServeWorker before normal
// CLI setup. No source files are opened by the worker.
type Worker struct {
	admission chan struct{} // Guards all mutable state; also serializes Close.
	ctx       context.Context
	cancel    context.CancelFunc
	command   func() *exec.Cmd
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	closed    bool
	stopWatch func() bool
}

// NewWorker creates a lazy worker scoped to ctx (the caller's overall budget).
// Admission, startup, parse, extraction and response share the request deadline,
// capped at WorkerParseTimeout. A child is reused until failure or Close.
func NewWorker(ctx context.Context) *Worker {
	life, cancel := context.WithCancel(ctx)
	w := &Worker{ctx: life, cancel: cancel, admission: make(chan struct{}, 1)}
	w.command = func() *exec.Cmd {
		exe, err := os.Executable()
		if err != nil {
			exe = ""
		}
		return exec.Command(exe, InternalWorkerArg)
	}
	// Also reap an idle worker when its overall budget expires.
	w.admission <- struct{}{}
	w.stopWatch = context.AfterFunc(ctx, func() { _ = w.Close() })
	<-w.admission
	return w
}

// ParseSource returns the same entries and syntax-error flag as Entries, but
// operates on already-read bytes. filename selects the extractor (including
// extensionless Dockerfile/Jenkinsfile); it is not opened. The caller owns safe,
// bounded regular-file reads and must not mutate src during this call.
func (w *Worker) ParseSource(ctx context.Context, filename string, src []byte) ([]Entry, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, WorkerParseTimeout)
	defer cancel()
	select {
	case w.admission <- struct{}{}:
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case <-w.ctx.Done():
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		return nil, false, ErrWorkerClosed
	}
	defer func() { <-w.admission }()
	// Cancellation can race with admission. It must not dispose an unrelated
	// request's worker, even if both select cases were ready.
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if w.closed {
		return nil, false, ErrWorkerClosed
	}
	fail := func(err error) ([]Entry, bool, error) { w.closeLocked(); return nil, false, err }
	if err := w.ctx.Err(); err != nil {
		return fail(err)
	}
	if len(src) > WorkerMaxSourceBytes || len(filename) > maxWorkerFilename {
		return fail(errors.New("codeindex worker request exceeds input limit"))
	}
	payload, err := json.Marshal(workerRequest{filename, src})
	if err != nil {
		return fail(err)
	}
	first := w.cmd == nil
	if first {
		w.cmd = w.command()
		w.stdin, err = w.cmd.StdinPipe()
		if err != nil {
			return fail(err)
		}
		w.stdout, err = w.cmd.StdoutPipe()
		if err != nil {
			return fail(err)
		}
		if err = w.cmd.Start(); err != nil {
			return fail(err)
		}
	}
	// Only IPC runs in this goroutine. On every exit path it is joined; killing
	// the child and closing both pipes interrupts blocked reads and writes.
	type result struct {
		response workerResponse
		err      error
	}
	done := make(chan result, 1)
	go func() {
		var response workerResponse
		exchange := func() error {
			if first {
				data, err := readFrame(w.stdout, maxWorkerResponse)
				if err != nil {
					return err
				}
				var ready workerResponse
				if err := json.Unmarshal(data, &ready); err != nil {
					return err
				}
				if !ready.Ready {
					return errors.New("codeindex worker missing ready handshake")
				}
			}
			if err := writeFrame(w.stdin, payload, maxWorkerRequest); err != nil {
				return err
			}
			data, err := readFrame(w.stdout, maxWorkerResponse)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(data, &response); err != nil {
				return err
			}
			if response.Ready {
				return errors.New("unexpected worker handshake")
			}
			if response.Error != "" {
				return errors.New(response.Error)
			}
			return nil
		}
		err := exchange()
		done <- result{response, err}
	}()
	select {
	case res := <-done:
		if err := w.ctx.Err(); err != nil {
			return fail(err)
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if res.err != nil {
			return fail(fmt.Errorf("codeindex worker: %w", res.err))
		}
		return res.response.Entries, res.response.HasError, nil
	case <-ctx.Done():
		w.closeLocked()
		<-done
		return nil, false, ctx.Err()
	case <-w.ctx.Done():
		w.closeLocked()
		<-done
		return nil, false, w.ctx.Err()
	}
}

// Close is idempotent and interrupts an active request before waiting for it.
func (w *Worker) Close() error {
	w.cancel()
	w.admission <- struct{}{}
	defer func() { <-w.admission }()
	w.closeLocked()
	return nil
}

func (w *Worker) closeLocked() {
	if w.closed {
		return
	}
	w.closed = true
	w.cancel()
	if w.stopWatch != nil {
		w.stopWatch()
	}
	if w.cmd != nil && w.cmd.Process != nil {
		_ = w.cmd.Process.Kill()
	}
	if w.stdin != nil {
		_ = w.stdin.Close()
	}
	if w.stdout != nil {
		_ = w.stdout.Close()
	}
	if w.cmd != nil && w.cmd.Process != nil {
		_ = w.cmd.Wait()
	}
}

func readFrame(r io.Reader, limit uint32) ([]byte, error) {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return nil, err
	}
	if n == 0 || n > limit {
		return nil, fmt.Errorf("invalid worker frame size %d (limit %d)", n, limit)
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}

func writeFrame(w io.Writer, b []byte, limit uint32) error {
	if len(b) == 0 || uint64(len(b)) > uint64(limit) {
		return errors.New("worker frame exceeds limit")
	}
	if err := binary.Write(w, binary.BigEndian, uint32(len(b))); err != nil {
		return err
	}
	n, err := w.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	return err
}

// ServeWorker runs the private framed protocol. All uninterruptible Bonsai work
// stays here; the parent enforces deadlines by killing and reaping this process.
func ServeWorker(in io.Reader, out io.Writer) error {
	if err := writeFrame(out, []byte(`{"ready":true}`), maxWorkerResponse); err != nil {
		return err
	}
	for {
		data, err := readFrame(in, maxWorkerRequest)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		var req workerRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return err
		}
		if len(req.Source) > WorkerMaxSourceBytes || len(req.Filename) > maxWorkerFilename {
			return errors.New("worker input exceeds limit")
		}
		entries, hasError, parseErr := entriesSource(req.Filename, req.Source)
		response := workerResponse{Entries: entries, HasError: hasError}
		if parseErr != nil {
			response.Error = parseErr.Error()
		}
		data, err = json.Marshal(response)
		if err != nil {
			return err
		}
		if err := writeFrame(out, data, maxWorkerResponse); err != nil {
			return err
		}
		if parseErr != nil {
			return parseErr
		}
	}
}
