package leadership

import (
	"context"
	"errors"
	"sync"
)

// FakeElector is a manually driven Elector for unit tests.
type FakeElector struct {
	tracker *Tracker

	mu        sync.Mutex
	callbacks Callbacks
	running   bool
	cancel    context.CancelFunc
	leadCtx   context.Context
	leadGen   uint64
}

func NewFakeElector(identity string) *FakeElector {
	return &FakeElector{tracker: NewTracker(identity)}
}

func (f *FakeElector) State() State { return f.tracker.State() }

func (f *FakeElector) Run(ctx context.Context, callbacks Callbacks) error {
	f.mu.Lock()
	if f.running {
		f.mu.Unlock()
		return errors.New("fake elector already running")
	}
	f.running = true
	f.callbacks = callbacks
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.running = false
		f.mu.Unlock()
	}()
	<-ctx.Done()
	f.ForceStop()
	return ctx.Err()
}

func (f *FakeElector) ForceLead() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.leadCtx != nil {
		return f.leadGen
	}
	generation := f.tracker.BeginLeading()
	leadCtx, cancel := context.WithCancel(context.Background())
	f.leadCtx = leadCtx
	f.cancel = cancel
	f.leadGen = generation
	cb := f.callbacks.OnStartedLeading
	if cb != nil {
		go cb(leadCtx, generation)
	}
	return generation
}

func (f *FakeElector) ForceStop() {
	f.mu.Lock()
	cancel := f.cancel
	generation := f.leadGen
	cb := f.callbacks.OnStoppedLeading
	f.leadCtx = nil
	f.cancel = nil
	f.leadGen = 0
	f.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if f.tracker.EndLeading(generation) && cb != nil {
		cb(generation)
	}
}

func (f *FakeElector) ForceNewLeader(identity string) {
	f.tracker.SetLeaderIdentity(identity)
	f.mu.Lock()
	cb := f.callbacks.OnNewLeader
	f.mu.Unlock()
	if cb != nil {
		cb(identity)
	}
}
