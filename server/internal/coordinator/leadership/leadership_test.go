package leadership

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestTrackerGenerationAndIdempotentStop(t *testing.T) {
	tracker := NewTracker("coord-a")
	first := tracker.BeginLeading()
	if first != 1 || !tracker.State().IsLeader {
		t.Fatalf("first acquire: %+v", tracker.State())
	}
	if tracker.EndLeading(99) {
		t.Fatal("stale generation stop succeeded")
	}
	if !tracker.EndLeading(first) {
		t.Fatal("matching generation stop failed")
	}
	if tracker.EndLeading(first) {
		t.Fatal("duplicate stop succeeded")
	}
	second := tracker.BeginLeading()
	if second != 2 {
		t.Fatalf("generation did not advance: %d", second)
	}
}

func TestFakeElectorAcquireLossReacquire(t *testing.T) {
	fake := NewFakeElector("coord-a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var started, stopped atomic.Uint64
	done := make(chan struct{})
	go func() {
		_ = fake.Run(ctx, Callbacks{
			OnStartedLeading: func(leadCtx context.Context, generation uint64) {
				started.Store(generation)
				<-leadCtx.Done()
			},
			OnStoppedLeading: func(generation uint64) {
				stopped.Store(generation)
			},
		})
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	gen := fake.ForceLead()
	waitFor(t, func() bool { return started.Load() == gen })
	if !fake.State().IsLeader {
		t.Fatal("expected leader")
	}
	fake.ForceStop()
	waitFor(t, func() bool { return stopped.Load() == gen })
	if fake.State().IsLeader {
		t.Fatal("expected follower after stop")
	}
	gen2 := fake.ForceLead()
	waitFor(t, func() bool { return started.Load() == gen2 })
	if gen2 <= gen {
		t.Fatalf("generation did not increase: %d -> %d", gen, gen2)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit")
	}
}

func waitFor(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met")
}
