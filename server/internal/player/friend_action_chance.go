package player

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	chapterv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/chapter"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/protobuf/proto"
)

const applyPestTaskID uint32 = 8

var (
	ErrInsufficientActionChance  = errors.New("insufficient friend action chance")
	ErrActionReservationConflict = errors.New("friend action reservation conflicts with existing interaction")
	ErrActionReservationMissing  = errors.New("friend action reservation is missing or consumed")
	ErrPlotNotEligible           = errors.New("plot is not eligible for friend action")
	ErrPestAlreadyPresent        = errors.New("pest is already present")
	ErrPestSourceForbidden       = errors.New("pest source may not catch its own pest")
)

func validChanceAction(action datav1.FriendInteractionAction) bool {
	switch action {
	case datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND,
		datav1.FriendInteractionAction_CATCH_PEST_FOR_FRIEND,
		datav1.FriendInteractionAction_HELP_CLEAN_FRIEND_PLOT:
		return true
	default:
		return false
	}
}

func actionChance(state *datav1.FriendActionState, action datav1.FriendInteractionAction) *uint32 {
	if state == nil {
		return nil
	}
	switch action {
	case datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND:
		return &state.ApplyPestChances
	case datav1.FriendInteractionAction_CATCH_PEST_FOR_FRIEND:
		return &state.CatchPestChances
	case datav1.FriendInteractionAction_HELP_CLEAN_FRIEND_PLOT:
		return &state.HelpCleanChances
	default:
		return nil
	}
}

func (r *Runtime) ReserveActionChance(
	ctx context.Context, visitorID, ownerEpoch uint64, interactionID []byte,
	action datav1.FriendInteractionAction,
) (alreadyReserved bool, err error) {
	if ownerEpoch == 0 {
		return false, ErrNotOwner
	}
	if len(interactionID) != 16 || !validChanceAction(action) {
		return false, errors.New("invalid friend action reservation request")
	}
	shardID := routing.ShardForPlayer(visitorID)
	r.shardLocks[shardID].RLock()
	defer r.shardLocks[shardID].RUnlock()
	a, err := r.actorFor(ctx, visitorID, ownerEpoch)
	if err != nil {
		return false, err
	}
	now := r.currentTime().UTC()
	stepKey := syncStepKey(syncStepReserveFriendAction, interactionID)
	var mailboxErr error
	var mutated bool
	if err := a.mailbox.Do(ctx, func() {
		alreadyReserved, mutated, mailboxErr = reserveActionChance(a.state, interactionID, action, now)
		if mailboxErr == nil && mutated {
			a.markSyncPending(stepKey, pendingSyncStep{revision: a.state.CheckpointRevision})
		}
	}); err != nil {
		return false, fmt.Errorf("execute reserve friend action mailbox: %w", err)
	}
	if mailboxErr != nil {
		return false, mailboxErr
	}
	if _, err := r.settleSyncStepLocked(ctx, visitorID, a, stepKey); err != nil {
		return false, fmt.Errorf("flush friend action reservation: %w", err)
	}
	return alreadyReserved, nil
}

func reserveActionChance(
	state *State, interactionID []byte, action datav1.FriendInteractionAction, now time.Time,
) (alreadyReserved, mutated bool, err error) {
	if state == nil || len(interactionID) != 16 || !validChanceAction(action) {
		return false, false, errors.New("invalid friend action reservation request")
	}
	mutated = migrateFriendSchema(state, now)
	for _, reservation := range state.FriendReservations {
		if !bytes.Equal(reservation.InteractionId, interactionID) {
			continue
		}
		if reservation.Action != action || reservation.ReservedActionChances != 1 {
			return false, mutated, ErrActionReservationConflict
		}
		return true, mutated, nil
	}
	chance := actionChance(state.FriendActions, action)
	var reserved uint64
	for _, reservation := range state.FriendReservations {
		if reservation.Action == action &&
			reservation.Status == datav1.FriendReservationStatus_FRIEND_RESERVATION_RESERVED {
			reserved += uint64(reservation.ReservedActionChances)
		}
	}
	if chance == nil || uint64(*chance) < reserved+1 {
		return false, mutated, ErrInsufficientActionChance
	}
	state.FriendReservations = append(state.FriendReservations, &datav1.FriendResourceReservation{
		InteractionId: append([]byte(nil), interactionID...), Action: action,
		Status:                datav1.FriendReservationStatus_FRIEND_RESERVATION_RESERVED,
		ReservedActionChances: 1, CreatedAtMs: now.UnixMilli(), UpdatedAtMs: now.UnixMilli(),
	})
	state.CheckpointRevision++
	state.UpdatedAtMS = now.UnixMilli()
	return false, true, nil
}

func (r *Runtime) CommitActionChance(
	ctx context.Context, visitorID, ownerEpoch uint64, interactionID []byte,
	action datav1.FriendInteractionAction, ownerResultPayload []byte,
) (response *wsv1.FriendActionResponse, alreadyCommitted bool, err error) {
	if ownerEpoch == 0 {
		return nil, false, ErrNotOwner
	}
	if len(interactionID) != 16 || !validChanceAction(action) {
		return nil, false, errors.New("invalid friend action commit request")
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
	var commitErr error
	if err := a.mailbox.Do(ctx, func() {
		if existing := findFriendReceipt(a.state.FriendReceipts, interactionID, datav1.FriendReceiptRole_FRIEND_RECEIPT_VISITOR); existing != nil {
			response = &wsv1.FriendActionResponse{}
			if proto.Unmarshal(existing.ResultPayload, response) != nil {
				commitErr = errors.New("stored friend action commit receipt is corrupt")
				return
			}
			alreadyCommitted = true
			return
		}
		migrateFriendSchema(a.state, now)
		var reservation *datav1.FriendResourceReservation
		for _, candidate := range a.state.FriendReservations {
			if bytes.Equal(candidate.InteractionId, interactionID) {
				reservation = candidate
				break
			}
		}
		if reservation == nil || reservation.Action != action ||
			reservation.Status != datav1.FriendReservationStatus_FRIEND_RESERVATION_RESERVED ||
			reservation.ReservedActionChances != 1 {
			commitErr = ErrActionReservationMissing
			return
		}
		chance := actionChance(a.state.FriendActions, action)
		if chance == nil || *chance == 0 {
			commitErr = ErrInsufficientActionChance
			return
		}
		*chance--
		reservation.Status = datav1.FriendReservationStatus_FRIEND_RESERVATION_CONSUMED
		reservation.UpdatedAtMs = now.UnixMilli()
		if action == datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND {
			incrementApplyPestTask(a.state)
		}
		var farmPatch *wsv1.FarmViewPatch
		if len(ownerResultPayload) > 0 {
			ownerResponse := &wsv1.FriendActionResponse{}
			if proto.Unmarshal(ownerResultPayload, ownerResponse) == nil {
				farmPatch = ownerResponse.FarmPatch
			}
		}
		response = &wsv1.FriendActionResponse{
			InteractionId: append([]byte(nil), interactionID...), FarmPatch: farmPatch,
		}
		if action == datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND {
			response.VisitorPatch = &wsv1.PlayerStatePatch{CurrentChapter: a.state.Snapshot().CurrentChapter}
		}
		body, marshalErr := proto.MarshalOptions{Deterministic: true}.Marshal(response)
		if marshalErr != nil {
			commitErr = fmt.Errorf("marshal friend action commit result: %w", marshalErr)
			return
		}
		digest := sha256.Sum256(body)
		a.state.FriendReceipts = append(a.state.FriendReceipts, &datav1.FriendInteractionReceipt{
			InteractionId: append([]byte(nil), interactionID...),
			Role:          datav1.FriendReceiptRole_FRIEND_RECEIPT_VISITOR, Action: action,
			Status:             datav1.FriendReceiptStatus_FRIEND_RECEIPT_COMMITTED,
			ResultDigestSha256: digest[:], ResultPayload: body, CommittedAtMs: now.UnixMilli(),
		})
		a.state.PlayerSeq++
		a.state.CheckpointRevision++
		a.state.UpdatedAtMS = now.UnixMilli()
		mutated = true
		a.markSyncPending(stepKey, pendingSyncStep{revision: a.state.CheckpointRevision})
	}); err != nil {
		return nil, false, fmt.Errorf("execute commit friend action mailbox: %w", err)
	}
	if commitErr != nil {
		return nil, false, commitErr
	}
	if !alreadyCommitted && !mutated {
		return nil, false, errors.New("commit friend action did not mutate visitor state")
	}
	if _, err := r.settleSyncStepLocked(ctx, visitorID, a, stepKey); err != nil {
		return nil, false, fmt.Errorf("flush friend action commit: %w", err)
	}
	return response, alreadyCommitted, nil
}

func (r *Runtime) ReleaseActionChance(
	ctx context.Context, visitorID, ownerEpoch uint64, interactionID []byte,
	action datav1.FriendInteractionAction,
) error {
	if ownerEpoch == 0 {
		return ErrNotOwner
	}
	if len(interactionID) != 16 || !validChanceAction(action) {
		return errors.New("invalid friend action release request")
	}
	shardID := routing.ShardForPlayer(visitorID)
	r.shardLocks[shardID].RLock()
	defer r.shardLocks[shardID].RUnlock()
	a, err := r.actorFor(ctx, visitorID, ownerEpoch)
	if err != nil {
		return err
	}
	now := r.currentTime().UTC()
	stepKey := syncStepKey(syncStepReleaseFriendAction, interactionID)
	if err := a.mailbox.Do(ctx, func() {
		for _, reservation := range a.state.FriendReservations {
			if !bytes.Equal(reservation.InteractionId, interactionID) ||
				reservation.Action != action ||
				reservation.Status != datav1.FriendReservationStatus_FRIEND_RESERVATION_RESERVED {
				continue
			}
			reservation.Status = datav1.FriendReservationStatus_FRIEND_RESERVATION_RELEASED
			reservation.UpdatedAtMs = now.UnixMilli()
			a.state.CheckpointRevision++
			a.state.UpdatedAtMS = now.UnixMilli()
			a.markSyncPending(stepKey, pendingSyncStep{revision: a.state.CheckpointRevision})
			return
		}
	}); err != nil {
		return fmt.Errorf("execute release friend action mailbox: %w", err)
	}
	if _, err := r.settleSyncStepLocked(ctx, visitorID, a, stepKey); err != nil {
		return fmt.Errorf("flush friend action release: %w", err)
	}
	return nil
}

func incrementApplyPestTask(state *State) {
	if state.Chapter != chapterv1.ChapterStatus_IN_PROGRESS {
		return
	}
	for i := range state.Tasks {
		if state.Tasks[i].ID != applyPestTaskID {
			continue
		}
		if state.Tasks[i].Current < state.Tasks[i].Target {
			state.Tasks[i].Current++
		}
		if allTasksComplete(state.Tasks) {
			state.Chapter = chapterv1.ChapterStatus_CLAIMABLE
		}
		return
	}
}
