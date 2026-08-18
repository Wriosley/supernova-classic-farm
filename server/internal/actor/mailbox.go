// Package actor provides the per-player serialized execution primitive used by Zone.
package actor

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

var ErrClosed = errors.New("actor mailbox closed")

var errMailboxFull = errors.New("actor mailbox full")

type job struct {
	run  func()
	done chan struct{}
}

// AwaitHandle coordinates a suspended mailbox job and its eventual
// continuation. It lets the worker release the mailbox between the initial
// step and the resumed step.
type AwaitHandle struct {
	mailbox *Mailbox
	done    chan struct{}

	mu        sync.Mutex
	suspended bool
	resumed   bool
	completed bool
	err       error
}

// Mailbox owns one worker goroutine. Jobs submitted to the same mailbox execute
// in arrival order, while independent mailboxes have independent workers.
type Mailbox struct {
	jobs      chan job
	closeOnce sync.Once
	closed    chan struct{}
	mu        sync.RWMutex
	isClosed  bool
	inflight  atomic.Int32 // queued + running admitted jobs
}

func NewMailbox(queueSize int) *Mailbox {
	if queueSize < 0 {
		queueSize = 0
	}
	m := &Mailbox{
		jobs:   make(chan job, queueSize),
		closed: make(chan struct{}),
	}
	go m.loop()
	return m
}

// BeginAwait runs fn on the mailbox worker. fn may call Suspend to indicate
// that completion will happen later and may call Resume from another goroutine
// to enqueue the continuation back onto the same mailbox. The returned handle
// becomes ready when the final step completes or fails.
func (m *Mailbox) BeginAwait(ctx context.Context, fn func(*AwaitHandle) error) (*AwaitHandle, error) {
	if fn == nil {
		return nil, errors.New("await function is required")
	}
	handle := &AwaitHandle{
		mailbox: m,
		done:    make(chan struct{}),
	}
	err := m.Do(ctx, func() {
		runErr := fn(handle)
		handle.mu.Lock()
		suspended := handle.suspended
		completed := handle.completed
		handle.mu.Unlock()
		switch {
		case runErr != nil:
			handle.Complete(runErr)
		case !suspended && !completed:
			handle.Complete(nil)
		}
	})
	if err != nil {
		return nil, err
	}
	return handle, nil
}

// Suspend marks the current await as waiting for an external result. It is
// safe to call once from the initial mailbox step before the resume goroutine
// is launched.
func (h *AwaitHandle) Suspend() {
	if h == nil {
		return
	}
	h.mu.Lock()
	if !h.completed {
		h.suspended = true
	}
	h.mu.Unlock()
}

// Resume enqueues the continuation onto the original mailbox. The continuation
// runs serially with all other jobs in the same mailbox. Resume must be called
// after Suspend.
func (h *AwaitHandle) Resume(ctx context.Context, cont func() error) error {
	if h == nil {
		return ErrClosed
	}
	if cont == nil {
		return errors.New("await continuation is required")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	h.mu.Lock()
	switch {
	case h.completed:
		h.mu.Unlock()
		return ErrClosed
	case !h.suspended:
		h.mu.Unlock()
		return errors.New("await handle is not suspended")
	case h.resumed:
		h.mu.Unlock()
		return errors.New("await handle already resumed")
	}
	h.resumed = true
	h.mu.Unlock()
	if err := h.mailbox.Submit(func() {
		if err := cont(); err != nil {
			h.Complete(err)
			return
		}
		h.Complete(nil)
	}); err != nil {
		h.mu.Lock()
		h.resumed = false
		h.mu.Unlock()
		return err
	}
	return nil
}

// Complete finalizes the await with err. It is idempotent; later calls are
// ignored.
func (h *AwaitHandle) Complete(err error) {
	if h == nil {
		return
	}
	h.mu.Lock()
	if h.completed {
		h.mu.Unlock()
		return
	}
	h.completed = true
	h.err = err
	close(h.done)
	h.mu.Unlock()
}

// Wait blocks until the await completes or ctx is canceled.
func (h *AwaitHandle) Wait(ctx context.Context) error {
	if h == nil {
		return ErrClosed
	}
	select {
	case <-h.done:
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Mailbox) loop() {
	defer close(m.closed)
	for {
		j := <-m.jobs
		if j.run == nil {
			return
		}
		j.run()
		close(j.done)
		m.inflight.Add(-1)
	}
}

// Idle reports whether the mailbox has no queued or running work.
func (m *Mailbox) Idle() bool {
	return m.inflight.Load() == 0
}

// Submit enqueues fn without waiting for it to finish. Once admitted, fn always
// runs to completion so later jobs cannot overtake it. Returns ErrClosed if the
// mailbox is closed, or errMailboxFull if the bounded queue rejects the job.
func (m *Mailbox) Submit(fn func()) error {
	j := job{run: fn, done: make(chan struct{})}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.isClosed {
		return ErrClosed
	}
	select {
	case m.jobs <- j:
		m.inflight.Add(1)
		return nil
	default:
		return errMailboxFull
	}
}

// Do waits until fn has run on the mailbox worker or ctx is canceled before
// admission. Once admitted, fn always finishes so later jobs cannot overtake it.
func (m *Mailbox) Do(ctx context.Context, fn func()) error {
	j := job{run: fn, done: make(chan struct{})}
	m.mu.RLock()
	if m.isClosed {
		m.mu.RUnlock()
		return ErrClosed
	}
	select {
	case m.jobs <- j:
		m.inflight.Add(1)
		m.mu.RUnlock()
	case <-ctx.Done():
		m.mu.RUnlock()
		return ctx.Err()
	}

	<-j.done
	return nil
}

func (m *Mailbox) Close() {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.isClosed = true
		m.mu.Unlock()
		m.jobs <- job{}
		<-m.closed
	})
}
