package player

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/protobuf/proto"
)

// ApplyVisitorFriendSideEffect credits the visitor after a successful owner
// ApplyVisitorAction on the direct (non-Saga) friend-action path:
//
//   - STEAL_FRIEND_CROP: apply frozen dog-bite penalty (no crop inventory)
//   - CATCH_PEST_FOR_FRIEND / HELP_CLEAN_FRIEND_PLOT: +1 coin
//   - APPLY_PEST_TO_FRIEND: random -10..+10 coins (frozen in VISITOR receipt)
//
// Idempotent via VISITOR FriendInteractionReceipt keyed by interactionID.
func (r *Runtime) ApplyVisitorFriendSideEffect(
	ctx context.Context,
	visitorID, ownerEpoch uint64,
	interactionID []byte,
	action datav1.FriendInteractionAction,
	ownerResultPayload []byte,
) (response *wsv1.FriendActionResponse, alreadyApplied bool, err error) {
	if ownerEpoch == 0 {
		return nil, false, ErrNotOwner
	}
	if len(interactionID) != 16 || visitorID == 0 {
		return nil, false, errors.New("invalid visitor friend side-effect request")
	}
	switch action {
	case datav1.FriendInteractionAction_STEAL_FRIEND_CROP,
		datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND,
		datav1.FriendInteractionAction_CATCH_PEST_FOR_FRIEND,
		datav1.FriendInteractionAction_HELP_CLEAN_FRIEND_PLOT:
	default:
		return nil, false, errors.New("unsupported friend side-effect action")
	}

	shardID := routing.ShardForPlayer(visitorID)
	r.shardLocks[shardID].RLock()
	defer r.shardLocks[shardID].RUnlock()
	a, err := r.actorFor(ctx, visitorID, ownerEpoch)
	if err != nil {
		return nil, false, err
	}
	now := r.currentTime().UTC()
	stepKey := syncStepKey(syncStepCommitFriendAction, interactionID)

	var mutated bool
	var sideErr error
	if execErr := a.mailbox.Do(ctx, func() {
		if existing := findFriendReceipt(
			a.state.FriendReceipts, interactionID, datav1.FriendReceiptRole_FRIEND_RECEIPT_VISITOR,
		); existing != nil {
			stored := &wsv1.FriendActionResponse{}
			if proto.Unmarshal(existing.ResultPayload, stored) != nil {
				sideErr = errors.New("stored visitor friend side-effect receipt is corrupt")
				return
			}
			response = stored
			alreadyApplied = true
			return
		}
		migrateFriendSchema(a.state, now)

		var stealGuard *wsv1.StealGuardOutcome
		if len(ownerResultPayload) > 0 {
			ownerResponse := &wsv1.FriendActionResponse{}
			if proto.Unmarshal(ownerResultPayload, ownerResponse) == nil {
				stealGuard = ownerResponse.StealGuard
			}
		}

		coinDelta := int64(0)
		switch action {
		case datav1.FriendInteractionAction_STEAL_FRIEND_CROP:
			if stealGuard != nil && stealGuard.GuardTriggered && stealGuard.GuardPenaltyConfigured > 0 {
				coinDelta = -stealGuard.GuardPenaltyConfigured
			}
		case datav1.FriendInteractionAction_CATCH_PEST_FOR_FRIEND,
			datav1.FriendInteractionAction_HELP_CLEAN_FRIEND_PLOT:
			coinDelta = 1
		case datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND:
			coinDelta = int64(r.rollIntn(21) - 10)
			incrementApplyPestTask(a.state)
		}

		appliedDelta := coinDelta
		if appliedDelta < 0 {
			penalty := -appliedDelta
			if a.state.Coins < penalty {
				penalty = a.state.Coins
			}
			a.state.Coins -= penalty
			appliedDelta = -penalty
		} else if appliedDelta > 0 {
			a.state.Coins += appliedDelta
		}

		balance := a.state.Coins
		coinBalance := &balance
		if stealGuard != nil && stealGuard.GuardTriggered {
			stealGuard = proto.Clone(stealGuard).(*wsv1.StealGuardOutcome)
			if appliedDelta < 0 {
				stealGuard.GuardPenaltyApplied = -appliedDelta
			} else {
				stealGuard.GuardPenaltyApplied = 0
			}
		}

		friendResponse := &wsv1.FriendActionResponse{
			InteractionId: append([]byte(nil), interactionID...),
			VisitorPatch: &wsv1.PlayerStatePatch{
				CoinBalance:    coinBalance,
				CurrentChapter: a.state.Snapshot().CurrentChapter,
			},
			StealGuard: stealGuard,
		}
		body, marshalErr := proto.MarshalOptions{Deterministic: true}.Marshal(friendResponse)
		if marshalErr != nil {
			sideErr = fmt.Errorf("marshal visitor friend side-effect: %w", marshalErr)
			return
		}
		digest := sha256.Sum256(body)
		a.state.FriendReceipts = append(a.state.FriendReceipts, &datav1.FriendInteractionReceipt{
			InteractionId:      append([]byte(nil), interactionID...),
			Role:               datav1.FriendReceiptRole_FRIEND_RECEIPT_VISITOR,
			Action:             action,
			Status:             datav1.FriendReceiptStatus_FRIEND_RECEIPT_COMMITTED,
			ResultDigestSha256: append([]byte(nil), digest[:]...),
			ResultPayload:      body,
			CommittedAtMs:      now.UnixMilli(),
		})
		a.state.PlayerSeq++
		a.state.CheckpointRevision++
		a.state.UpdatedAtMS = now.UnixMilli()
		mutated = true
		a.markSyncPending(stepKey, pendingSyncStep{revision: a.state.CheckpointRevision})
		response = friendResponse
	}); execErr != nil {
		return nil, false, fmt.Errorf("execute visitor friend side-effect mailbox: %w", execErr)
	}
	if sideErr != nil {
		return nil, false, sideErr
	}
	if !alreadyApplied && !mutated {
		return nil, false, errors.New("visitor friend side-effect did not mutate state")
	}
	if _, err := r.settleSyncStepLocked(ctx, visitorID, a, stepKey); err != nil {
		return nil, false, fmt.Errorf("flush visitor friend side-effect: %w", err)
	}
	r.refreshActorDeadline(visitorID, a)
	return response, alreadyApplied, nil
}
