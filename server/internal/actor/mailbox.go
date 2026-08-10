// Package actor provides the per-player serialized execution primitive used by Zone.
package actor

import (
	"context"
	"errors"
	"sync"
)

var ErrClosed = errors.New("actor mailbox closed")

var errMailboxFull = errors.New("actor mailbox full")

type job struct {
	run  func()
	done chan struct{}
}

// Mailbox owns one worker goroutine. Jobs submitted to the same mailbox execute
// in arrival order, while independent mailboxes have independent workers.
type Mailbox struct {
	jobs      chan job
	closeOnce sync.Once
	closed    chan struct{}
	mu        sync.RWMutex
	isClosed  bool
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

func (m *Mailbox) loop() {
	defer close(m.closed)
	for {
		j := <-m.jobs
		if j.run == nil {
			return
		}
		j.run()
		close(j.done)
	}
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
