package player

import (
	"container/heap"
	"context"
	"sync"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

type actorDeadline struct {
	playerID   uint64
	deadline   time.Time
	generation uint64
	index      int
}

type actorDeadlineHeap []*actorDeadline

func (h actorDeadlineHeap) Len() int { return len(h) }

func (h actorDeadlineHeap) Less(i, j int) bool {
	if h[i].deadline.Equal(h[j].deadline) {
		return h[i].playerID < h[j].playerID
	}
	return h[i].deadline.Before(h[j].deadline)
}

func (h actorDeadlineHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *actorDeadlineHeap) Push(x any) {
	item := x.(*actorDeadline)
	item.index = len(*h)
	*h = append(*h, item)
}

func (h *actorDeadlineHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*h = old[:n-1]
	return item
}

// actorDeadlineBook is the shared min-heap of Actor maturity deadlines.
// Stale generations remain in the heap until popped and ignored.
type actorDeadlineBook struct {
	mu     sync.Mutex
	heap   actorDeadlineHeap
	latest map[uint64]uint64 // playerID -> latest generation
	wake   chan struct{}
}

func newActorDeadlineBook() *actorDeadlineBook {
	return &actorDeadlineBook{
		latest: make(map[uint64]uint64),
		wake:   make(chan struct{}, 1),
	}
}

func (b *actorDeadlineBook) schedule(playerID uint64, deadline time.Time, generation uint64) {
	if playerID == 0 || generation == 0 || deadline.IsZero() {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.latest[playerID] = generation
	heap.Push(&b.heap, &actorDeadline{
		playerID: playerID, deadline: deadline.UTC(), generation: generation,
	})
	b.signalLocked()
}

func (b *actorDeadlineBook) cancel(playerID uint64) {
	if playerID == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.latest, playerID)
	b.signalLocked()
}

func (b *actorDeadlineBook) signalLocked() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

func (b *actorDeadlineBook) peek() (actorDeadline, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for len(b.heap) > 0 {
		top := b.heap[0]
		if b.latest[top.playerID] != top.generation {
			heap.Pop(&b.heap)
			continue
		}
		return *top, true
	}
	return actorDeadline{}, false
}

func (b *actorDeadlineBook) popDue(now time.Time) (actorDeadline, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for len(b.heap) > 0 {
		top := b.heap[0]
		if b.latest[top.playerID] != top.generation {
			heap.Pop(&b.heap)
			continue
		}
		if top.deadline.After(now) {
			return actorDeadline{}, false
		}
		item := heap.Pop(&b.heap).(*actorDeadline)
		return *item, true
	}
	return actorDeadline{}, false
}

func (b *actorDeadlineBook) nextDelay(now time.Time) (time.Duration, bool) {
	next, ok := b.peek()
	if !ok {
		return 0, false
	}
	if !next.deadline.After(now) {
		return 0, true
	}
	return next.deadline.Sub(now), true
}

func (r *Runtime) schedule(playerID uint64, deadline time.Time, generation uint64) {
	if r == nil || r.deadlines == nil {
		return
	}
	r.deadlines.schedule(playerID, deadline, generation)
}

func (r *Runtime) cancelActorDeadline(playerID uint64) {
	if r == nil || r.deadlines == nil {
		return
	}
	r.deadlines.cancel(playerID)
}

// refreshActorDeadline reschedules or cancels the Actor's maturity deadline.
// Call after mailbox work that may have changed EstimatedMatureAtMS.
func (r *Runtime) refreshActorDeadline(playerID uint64, a *runtimeActor) {
	if r == nil || a == nil {
		return
	}
	var deadline time.Time
	var ok bool
	if err := a.mailbox.Do(r.backgroundCtx, func() { deadline, ok = a.nextTickAt() }); err != nil {
		return
	}
	if !ok {
		r.cancelActorDeadline(playerID)
		return
	}
	generation := a.tickGeneration.Add(1)
	r.schedule(playerID, deadline, generation)
}

// refreshActorDeadlineOwned is used only while the caller already owns the
// Actor mailbox (activation), so it must not submit a nested mailbox job.
func (r *Runtime) refreshActorDeadlineOwned(playerID uint64, a *runtimeActor) {
	deadline, ok := a.nextTickAt()
	if !ok {
		r.cancelActorDeadline(playerID)
		return
	}
	generation := a.tickGeneration.Add(1)
	r.schedule(playerID, deadline, generation)
}

func (r *Runtime) runDeadlineScheduler(ctx context.Context) {
	defer r.wg.Done()
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	for {
		waiting := false
		if next, ok := r.deadlines.peek(); ok {
			now := r.currentTime().UTC()
			delay := next.deadline.Sub(now)
			if delay < 0 {
				delay = 0
			}
			timer.Reset(delay)
			waiting = true
		}
		select {
		case <-ctx.Done():
			if waiting && !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-r.deadlines.wake:
			if waiting && !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
			r.fireDueDeadlines(ctx)
		}
	}
}

func (r *Runtime) fireDueDeadlines(ctx context.Context) {
	now := r.currentTime().UTC()
	for {
		item, ok := r.deadlines.popDue(now)
		if !ok {
			return
		}
		r.deliverActorTick(ctx, item)
	}
}

func (r *Runtime) deliverActorTick(ctx context.Context, item actorDeadline) {
	shardID := routing.ShardForPlayer(item.playerID)
	r.shardLocks[shardID].RLock()
	defer r.shardLocks[shardID].RUnlock()

	r.mu.Lock()
	a := r.actors[item.playerID]
	r.mu.Unlock()
	if a == nil || a.lifecycle.Load() != actorLifecycleReady || a.state == nil {
		return
	}
	if a.tickGeneration.Load() != item.generation {
		return
	}

	var result ActorTickResult
	var tickErr error
	if err := a.mailbox.Do(ctx, func() {
		if a.lifecycle.Load() != actorLifecycleReady || a.state == nil {
			return
		}
		if a.tickGeneration.Load() != item.generation {
			return
		}
		result, tickErr = a.tick(r.currentTime().UTC())
	}); err != nil || tickErr != nil {
		return
	}
	if len(result.MaturityEvents) > 0 {
		r.markDirty(item.playerID, result.DirtyRevision)
		_ = r.forwardMaturityEvents(ctx, result.MaturityEvents)
		r.publishFarmViewChanges(ctx, a, item.playerID, result.DomainChanges)
	}
	r.refreshActorDeadline(item.playerID, a)
}
