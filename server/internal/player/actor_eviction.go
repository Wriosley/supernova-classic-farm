package player

import (
	"bytes"
	"context"
	"fmt"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

// actorIdleTimeout is the continuous idle window required before an Actor may
// be safely recycled. Tests inject time rather than sleeping.
const actorIdleTimeout = 3 * time.Minute

func (a *runtimeActor) touchAccess(now time.Time) {
	if a == nil || now.IsZero() {
		return
	}
	a.lastAccessAtMS.Store(now.UTC().UnixMilli())
}

func (a *runtimeActor) lastAccessAt() time.Time {
	if a == nil {
		return time.Time{}
	}
	ms := a.lastAccessAtMS.Load()
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// EvictIdleActors walks resident Actors and removes those that have stayed
// idle for actorIdleTimeout: no owner connection, no live visitors, idle
// mailbox, and no recent external access. Dirty state is SaveCAS'd before
// delete; flush failure keeps the Actor resident.
func (r *Runtime) EvictIdleActors(ctx context.Context, now time.Time) error {
	if r == nil {
		return nil
	}
	now = now.UTC()
	r.mu.Lock()
	playerIDs := make([]uint64, 0, len(r.actors))
	for playerID := range r.actors {
		playerIDs = append(playerIDs, playerID)
	}
	r.mu.Unlock()
	for _, playerID := range playerIDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.evictIdleActor(ctx, playerID, now); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) evictIdleActor(ctx context.Context, playerID uint64, now time.Time) error {
	shardID := routing.ShardForPlayer(playerID)
	r.shardLocks[shardID].Lock()
	defer r.shardLocks[shardID].Unlock()

	r.mu.Lock()
	a := r.actors[playerID]
	presence := r.presence
	observers := r.observers
	r.mu.Unlock()
	if a == nil || a.lifecycle.Load() != actorLifecycleReady || a.state == nil {
		return nil
	}
	if !r.actorIdleEligible(a, playerID, now, presence, observers) {
		return nil
	}
	if !a.lifecycle.CompareAndSwap(actorLifecycleReady, actorLifecycleEvicting) {
		return nil
	}

	var (
		abort            bool
		checkpoint       *datav1.PlayerCheckpointV1
		expectedRevision uint64
		expectedToken    StoreToken
		checkpointErr    error
		tickResult       ActorTickResult
	)
	if err := a.mailbox.Do(ctx, func() {
		if a.lifecycle.Load() != actorLifecycleEvicting || a.state == nil {
			abort = true
			return
		}
		r.mu.Lock()
		presence = r.presence
		observers = r.observers
		r.mu.Unlock()
		// Recheck user-activity signals inside the mailbox. Inflight is
		// non-zero because this eviction job is running; Idle is checked
		// only before marking Evicting.
		if (presence != nil && presence.Has(playerID)) ||
			(observers != nil && observers.HasVisitors(playerID, now)) ||
			now.Sub(a.lastAccessAt()) < actorIdleTimeout {
			abort = true
			a.lifecycle.Store(actorLifecycleReady)
			return
		}
		var tickErr error
		tickResult, tickErr = a.tick(now)
		if tickErr != nil {
			checkpointErr = tickErr
			a.lifecycle.Store(actorLifecycleReady)
			abort = true
			return
		}
		checkpoint, checkpointErr = a.state.Checkpoint()
		if checkpointErr != nil {
			a.lifecycle.Store(actorLifecycleReady)
			abort = true
			return
		}
		expectedRevision = a.persistedRevision
		expectedToken = cloneStoreToken(a.persistedToken)
	}); err != nil {
		a.lifecycle.Store(actorLifecycleReady)
		r.refreshActorDeadline(playerID, a)
		return fmt.Errorf("evict player %d mailbox: %w", playerID, err)
	}
	if abort {
		r.refreshActorDeadline(playerID, a)
		return nil
	}
	if checkpointErr != nil {
		r.refreshActorDeadline(playerID, a)
		return fmt.Errorf("evict player %d checkpoint: %w", playerID, checkpointErr)
	}
	if len(tickResult.MaturityEvents) > 0 {
		r.markDirty(playerID, tickResult.DirtyRevision)
		_ = r.forwardMaturityEvents(ctx, tickResult.MaturityEvents)
		r.publishFarmViewChanges(ctx, a, playerID, tickResult.DomainChanges)
	}

	needsFlush := r.store != nil && checkpoint != nil &&
		checkpoint.CheckpointRevision > expectedRevision
	if needsFlush {
		result, saveErr := r.store.SaveCAS(ctx, CheckpointWrite{
			Checkpoint:       checkpoint,
			ExpectedRevision: expectedRevision,
			ExpectedToken:    expectedToken,
		})
		if writeErr := checkpointWriteError(result, saveErr); writeErr != nil {
			loaded, committed := r.checkpointWasCommitted(ctx, playerID, checkpoint)
			if !committed {
				a.lifecycle.Store(actorLifecycleReady)
				r.refreshActorDeadline(playerID, a)
				return fmt.Errorf("evict player %d flush: %w", playerID, writeErr)
			}
			result.NewToken = cloneStoreToken(loaded.Token)
		}
		if err := a.mailbox.Do(ctx, func() {
			if a.persistedRevision == expectedRevision &&
				bytes.Equal(a.persistedToken, expectedToken) {
				a.persistedRevision = checkpoint.CheckpointRevision
				a.persistedToken = cloneStoreToken(result.NewToken)
			}
		}); err != nil {
			a.lifecycle.Store(actorLifecycleReady)
			r.refreshActorDeadline(playerID, a)
			return fmt.Errorf("evict player %d acknowledge: %w", playerID, err)
		}
		r.mu.Lock()
		if r.dirtyRevision[playerID] <= checkpoint.CheckpointRevision {
			delete(r.dirtyRevision, playerID)
		}
		r.mu.Unlock()
	}

	r.cancelActorDeadline(playerID)
	r.removeActorIfSame(playerID, a)
	go a.mailbox.Close()
	return nil
}

func (r *Runtime) actorIdleEligible(
	a *runtimeActor,
	playerID uint64,
	now time.Time,
	presence PlayerPresence,
	observers FarmObservers,
) bool {
	if a == nil || !a.mailbox.Idle() {
		return false
	}
	if presence != nil && presence.Has(playerID) {
		return false
	}
	if observers != nil && observers.HasVisitors(playerID, now) {
		return false
	}
	last := a.lastAccessAt()
	if last.IsZero() || now.Sub(last) < actorIdleTimeout {
		return false
	}
	return true
}
