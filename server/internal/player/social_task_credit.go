package player

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	chapterv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/chapter"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

// addFriendTaskID is the well-known task ID for TASK_ADD_FRIEND in the
// starter chapter (see initialChapter in checkpoint.go).
const addFriendTaskID uint32 = 6

// friendActionInitialChances seeds every friend-interaction counter the first
// time a player's checkpoint is migrated to carry friend state. Phase 2 does
// not spend these; they exist so the schema migration itself is exercised.
const friendActionInitialChances uint32 = 100

// ApplyFriendTaskCredit idempotently credits one TASK_ADD_FRIEND completion
// for playerID, keyed by relationID so FriendSvr's Saga can retry safely. It
// runs on the player's Actor mailbox and, per the Friend design, flushes the
// resulting checkpoint synchronously rather than waiting for the periodic
// dirty flusher: FriendSvr's Saga only marks its own step APPLIED after this
// call returns, so a durable write here is required before that happens.
// A retry whose credit receipt is still only in Actor memory (an earlier
// attempt's SaveCAS failed) re-attempts that write and keeps failing until it
// commits, so newlyApplied=false never stands in for an unpersisted credit.
func (r *Runtime) ApplyFriendTaskCredit(
	ctx context.Context,
	playerID uint64,
	ownerEpoch uint64,
	relationID []byte,
) (newlyApplied bool, playerSeq uint64, err error) {
	if ownerEpoch == 0 {
		return false, 0, ErrNotOwner
	}
	if len(relationID) != 16 {
		return false, 0, errors.New("friend relation ID must be 16 bytes")
	}
	shardID := routing.ShardForPlayer(playerID)
	r.shardLocks[shardID].RLock()
	defer r.shardLocks[shardID].RUnlock()

	a, err := r.actorFor(ctx, playerID, ownerEpoch)
	if err != nil {
		return false, 0, err
	}
	now := r.now().UTC()
	stepKey := syncStepKey(syncStepFriendTaskCredit, relationID)
	var mailboxErr error
	if err := a.mailbox.Do(ctx, func() {
		beforeRevision := a.state.CheckpointRevision
		newlyApplied, mailboxErr = applyFriendTaskCredit(a.state, relationID, now)
		playerSeq = a.state.PlayerSeq
		if mailboxErr == nil && a.state.CheckpointRevision != beforeRevision {
			a.markSyncPending(stepKey, pendingSyncStep{revision: a.state.CheckpointRevision})
		}
	}); err != nil {
		return false, 0, fmt.Errorf("execute friend task credit mailbox: %w", err)
	}
	if mailboxErr != nil {
		return false, 0, mailboxErr
	}
	if _, err := r.settleSyncStepLocked(ctx, playerID, a, stepKey); err != nil {
		return false, 0, fmt.Errorf("flush friend task credit: %w", err)
	}
	return newlyApplied, playerSeq, nil
}

// applyFriendTaskCredit mutates state under the caller's Actor mailbox. It is
// split out from ApplyFriendTaskCredit so link_saga-style tests can exercise
// the pure state transition without a Runtime.
func applyFriendTaskCredit(state *State, relationID []byte, now time.Time) (bool, error) {
	if state == nil {
		return false, errors.New("player state is required")
	}
	migrateFriendSchema(state, now)
	for _, receipt := range state.FriendTaskCreditReceipts {
		if bytes.Equal(receipt.RelationId, relationID) {
			return false, nil
		}
	}
	incrementAddFriendTask(state)
	state.FriendTaskCreditReceipts = append(state.FriendTaskCreditReceipts, &datav1.FriendTaskCreditReceipt{
		RelationId: append([]byte(nil), relationID...), AppliedAtMs: now.UnixMilli(),
	})
	state.PlayerSeq++
	state.CheckpointRevision++
	state.UpdatedAtMS = now.UnixMilli()
	return true, nil
}

// migrateFriendSchema lazily upgrades a v1 (pre-friend) checkpoint's
// in-memory projection the first time any friend-related event reaches this
// player, bumping checkpoint_revision as its own durable step.
func migrateFriendSchema(state *State, now time.Time) bool {
	if state.FriendActions != nil {
		return false
	}
	state.FriendActions = &datav1.FriendActionState{
		ApplyPestChances: friendActionInitialChances,
		CatchPestChances: friendActionInitialChances,
		HelpCleanChances: friendActionInitialChances,
	}
	state.CheckpointRevision++
	state.UpdatedAtMS = now.UnixMilli()
	return true
}

// incrementAddFriendTask advances TASK_ADD_FRIEND's progress by one, capped
// at its target, and promotes the chapter to CLAIMABLE once every task in it
// is complete. It is a no-op once the chapter has left IN_PROGRESS or has no
// TASK_ADD_FRIEND entry (e.g. a later chapter).
func incrementAddFriendTask(state *State) {
	if state.Chapter != chapterv1.ChapterStatus_IN_PROGRESS {
		return
	}
	found := false
	for i := range state.Tasks {
		if state.Tasks[i].ID != addFriendTaskID {
			continue
		}
		if state.Tasks[i].Current < state.Tasks[i].Target {
			state.Tasks[i].Current++
		}
		found = true
		break
	}
	if !found {
		return
	}
	if allTasksComplete(state.Tasks) {
		state.Chapter = chapterv1.ChapterStatus_CLAIMABLE
	}
}
