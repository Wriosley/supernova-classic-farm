package player

import (
	"context"
	"errors"
	"fmt"
)

// ErrCheckpointNotDurable reports that a synchronous Saga step could not
// prove its Actor mutation reached the checkpoint store, so the caller must
// treat the step as unfinished and retry it with the same ID. It never means
// "already applied": the mutation lives in Actor memory only.
var ErrCheckpointNotDurable = errors.New("player checkpoint step is not durable")

// syncStepKind namespaces a durable-pending marker, because the visitor's
// reserve/commit/release steps all key off the same interaction ID on the
// same Actor.
type syncStepKind uint8

const (
	syncStepReserveSteal syncStepKind = iota + 1
	syncStepApplyStealOnOwner
	syncStepCommitSteal
	syncStepReleaseSteal
	syncStepFriendTaskCredit
	syncStepReserveFriendAction
	syncStepApplyPestOnOwner
	syncStepCommitFriendAction
	syncStepReleaseFriendAction
	syncStepCatchPestOnOwner
	syncStepHelpCleanOnOwner
)

// pendingSyncStep is the durable-pending marker a synchronous Saga step
// leaves on its runtimeActor between mutating Actor memory and proving that
// mutation reached the store.
//
// It exists because the idempotency markers those steps write (a
// FriendResourceReservation, an OWNER/VISITOR FriendInteractionReceipt, a
// FriendTaskCreditReceipt) live in the same Actor memory as the mutation
// itself: without this marker a retry that finds one of them cannot tell a
// durably committed step from a step whose SaveCAS failed, and would report
// success for state that is not persisted.
type pendingSyncStep struct {
	// revision is the state.CheckpointRevision the mutation produced. The
	// step is durable once persistedRevision reaches it, because every
	// checkpoint write is a full-state snapshot at its own revision.
	revision uint64
	// farmViewPlotIDs is non-empty when the step still owes visitors a
	// FarmViewPatch for those plots. The broadcast is deliberately deferred
	// until the mutation is durable, so it is emitted by whichever attempt
	// retires this marker: exactly once, with exactly one farm_view_seq bump.
	farmViewPlotIDs []uint32
}

func syncStepKey(kind syncStepKind, id []byte) string {
	key := make([]byte, 0, len(id)+1)
	key = append(key, byte(kind))
	return string(append(key, id...))
}

// markSyncPending records that this Actor holds an unproven mutation for
// key. Callers must run it inside the Actor's mailbox, in the same job that
// performed the mutation.
func (a *runtimeActor) markSyncPending(key string, step pendingSyncStep) {
	if a.syncPending == nil {
		a.syncPending = make(map[string]pendingSyncStep, 1)
	}
	a.syncPending[key] = step
}

// settleSyncStepLocked makes the mutation behind a durable-pending marker
// provably durable and then retires the marker. Every synchronous friend
// Saga step goes through it on its first attempt and on every same-ID retry,
// so a successful return always means "this state is in the store under the
// current owner epoch" instead of "this state is in Actor memory".
//
// A missing marker means there is nothing to prove: either the step mutated
// nothing, or an earlier attempt already retired the marker after a
// confirmed write, or the marker came out of a durable checkpoint at
// activation. Ordinary async Dirty writes are untouched: only revisions a
// synchronous step marked are ever forced out early, and they are flushed
// through the same flushPlayerLocked path (whole-state snapshot taken inside
// the mailbox, CAS against the Actor's persisted revision/token) so they
// cannot interleave with or overtake the periodic flusher.
//
// Callers must hold the player's shard read lock, like every other
// flushPlayerLocked caller. The returned plot IDs are the FarmViewPatch this
// call now owns and must broadcast.
func (r *Runtime) settleSyncStepLocked(
	ctx context.Context, playerID uint64, a *runtimeActor, key string,
) (farmViewPlotIDs []uint32, err error) {
	var pending pendingSyncStep
	var found bool
	var persistedRevision, currentRevision uint64
	if err := a.mailbox.Do(ctx, func() {
		pending, found = a.syncPending[key]
		persistedRevision = a.persistedRevision
		currentRevision = a.state.CheckpointRevision
	}); err != nil {
		return nil, fmt.Errorf("inspect pending step for player %d: %w", playerID, err)
	}
	if !found {
		return nil, nil
	}
	// Without a store nothing is durable by construction (development
	// runtime): the marker is retired so behavior matches the store-backed
	// path minus the write.
	if r.store != nil && persistedRevision < pending.revision {
		r.markDirty(playerID, currentRevision)
		if err := r.flushPlayerLocked(ctx, playerID); err != nil {
			return nil, err
		}
		if err := a.mailbox.Do(ctx, func() {
			persistedRevision = a.persistedRevision
		}); err != nil {
			return nil, fmt.Errorf("confirm durable checkpoint for player %d: %w", playerID, err)
		}
		if persistedRevision < pending.revision {
			return nil, fmt.Errorf(
				"%w: player %d persisted revision %d is below %d",
				ErrCheckpointNotDurable, playerID, persistedRevision, pending.revision,
			)
		}
	}
	// Retiring the marker inside the mailbox makes concurrent retries of the
	// same step agree on a single owner for the deferred broadcast.
	if err := a.mailbox.Do(ctx, func() {
		if _, stillPending := a.syncPending[key]; !stillPending {
			return
		}
		delete(a.syncPending, key)
		farmViewPlotIDs = pending.farmViewPlotIDs
	}); err != nil {
		return nil, fmt.Errorf("retire pending step for player %d: %w", playerID, err)
	}
	return farmViewPlotIDs, nil
}
