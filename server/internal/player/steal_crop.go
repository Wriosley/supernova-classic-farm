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

// stealTaskID is TASK_STEAL_CROP's well-known task ID in the second
// chapter's task list (see developmentNextChapterID in config.go).
const stealTaskID uint32 = 7

var (
	// ErrStealReservationConflict reports a same-interaction retry whose
	// item/quantity does not match the reservation already on file: a
	// semantic conflict the caller must reject rather than silently reuse.
	ErrStealReservationConflict = errors.New("steal reservation conflicts with an existing interaction")
	// ErrStealInventoryCapacity reports that the visitor's inventory type or
	// stack limit would be exceeded by this reservation, accounting for
	// every other currently RESERVED interaction's reserved capacity.
	ErrStealInventoryCapacity = errors.New("steal reservation exceeds inventory capacity")
	// ErrStealNotAvailable is the deterministic, non-owner-mutating
	// rejection documented in docs/contracts/idempotency-and-errors.md
	// (STEAL_NOT_AVAILABLE): the plot failed player.CanSteal.
	ErrStealNotAvailable = errors.New("steal is not available for this plot")
	// ErrStealReservationMissing reports that CommitSteal was called
	// without a matching live RESERVED reservation for interactionID.
	ErrStealReservationMissing = errors.New("steal reservation is missing or already consumed")
)

// ReserveSteal runs on the visitor's Actor mailbox and durably (synchronous
// SaveCAS) records intent to steal quantity units of cropItemID under
// interactionID, per docs/plans/friend_design_plan/03-好友互动Saga详细设计.md
// §5 step 2. It is idempotent: retrying the exact same
// (interactionID, cropItemID, quantity) tuple returns alreadyReserved=true
// without mutating the checkpoint again; a same-ID call with different
// fields returns ErrStealReservationConflict. A retry that finds the
// reservation only in Actor memory (because an earlier attempt's SaveCAS
// failed) re-attempts the write and still fails if it cannot commit: success
// always means the reservation is durable. Unlike ordinary commands this
// only bumps checkpoint_revision (the reservation is not visible business
// state, see data-model.md §18), never player_seq.
func (r *Runtime) ReserveSteal(
	ctx context.Context,
	visitorID, ownerEpoch uint64,
	interactionID []byte,
	cropItemID, quantity uint32,
) (alreadyReserved bool, err error) {
	if ownerEpoch == 0 {
		return false, ErrNotOwner
	}
	shardID := routing.ShardForPlayer(visitorID)
	r.shardLocks[shardID].RLock()
	defer r.shardLocks[shardID].RUnlock()
	a, err := r.actorFor(ctx, visitorID, ownerEpoch)
	if err != nil {
		return false, err
	}
	now := r.now().UTC()
	stepKey := syncStepKey(syncStepReserveSteal, interactionID)
	var mailboxErr error
	if execErr := a.mailbox.Do(ctx, func() {
		var mutated bool
		alreadyReserved, mutated, mailboxErr = reserveSteal(a.state, interactionID, cropItemID, quantity, now)
		if mailboxErr == nil && mutated {
			a.markSyncPending(stepKey, pendingSyncStep{revision: a.state.CheckpointRevision})
		}
	}); execErr != nil {
		return false, fmt.Errorf("execute reserve steal mailbox: %w", execErr)
	}
	if mailboxErr != nil {
		return false, mailboxErr
	}
	if _, err := r.settleSyncStepLocked(ctx, visitorID, a, stepKey); err != nil {
		return false, fmt.Errorf("flush steal reservation: %w", err)
	}
	return alreadyReserved, nil
}

// reserveSteal mutates state under the caller's Actor mailbox. It is split
// out from ReserveSteal so tests can exercise the pure state transition
// without a Runtime, matching applyFriendTaskCredit's convention.
func reserveSteal(
	state *State,
	interactionID []byte,
	cropItemID, quantity uint32,
	now time.Time,
) (alreadyReserved bool, mutated bool, err error) {
	if state == nil {
		return false, false, errors.New("player state is required")
	}
	if len(interactionID) != 16 || cropItemID == 0 || quantity == 0 {
		return false, false, errors.New("invalid steal reservation request")
	}
	mutated = migrateFriendSchema(state, now)
	for _, reservation := range state.FriendReservations {
		if !bytes.Equal(reservation.InteractionId, interactionID) {
			continue
		}
		if reservation.Action != datav1.FriendInteractionAction_STEAL_FRIEND_CROP ||
			reservation.GetReservedInventoryItemId() != cropItemID ||
			reservation.GetReservedInventoryQuantity() != quantity {
			return false, mutated, ErrStealReservationConflict
		}
		return true, mutated, nil
	}
	currentQuantity := state.Inventory[cropItemID]
	liveReserved := reservedQuantityForItem(state, cropItemID)
	if uint64(currentQuantity)+liveReserved+uint64(quantity) > inventoryStackLimit {
		return false, mutated, ErrStealInventoryCapacity
	}
	if currentQuantity == 0 && liveReserved == 0 &&
		projectedInventoryTypeCount(state) >= inventoryTypeLimit {
		return false, mutated, ErrStealInventoryCapacity
	}
	state.FriendReservations = append(state.FriendReservations, &datav1.FriendResourceReservation{
		InteractionId:             append([]byte(nil), interactionID...),
		Action:                    datav1.FriendInteractionAction_STEAL_FRIEND_CROP,
		Status:                    datav1.FriendReservationStatus_FRIEND_RESERVATION_RESERVED,
		ReservedInventoryItemId:   uint32Ptr(cropItemID),
		ReservedInventoryQuantity: uint32Ptr(quantity),
		CreatedAtMs:               now.UnixMilli(), UpdatedAtMs: now.UnixMilli(),
	})
	state.CheckpointRevision++
	state.UpdatedAtMS = now.UnixMilli()
	return false, true, nil
}

// reservedQuantityForItem sums every currently RESERVED reservation's
// quantity for itemID, so a new reservation's capacity check accounts for
// concurrent in-flight interactions the checkpoint has not yet consumed.
func reservedQuantityForItem(state *State, itemID uint32) uint64 {
	var total uint64
	for _, reservation := range state.FriendReservations {
		if reservation.Status != datav1.FriendReservationStatus_FRIEND_RESERVATION_RESERVED ||
			reservation.ReservedInventoryItemId == nil ||
			reservation.GetReservedInventoryItemId() != itemID {
			continue
		}
		total += uint64(reservation.GetReservedInventoryQuantity())
	}
	return total
}

// projectedInventoryTypeCount counts distinct item IDs the visitor's
// inventory would contain once every currently RESERVED reservation is
// consumed, bounding INVENTORY_TYPE_LIMIT against in-flight interactions.
func projectedInventoryTypeCount(state *State) int {
	types := make(map[uint32]struct{}, len(state.Inventory))
	for itemID := range state.Inventory {
		types[itemID] = struct{}{}
	}
	for _, reservation := range state.FriendReservations {
		if reservation.Status == datav1.FriendReservationStatus_FRIEND_RESERVATION_RESERVED &&
			reservation.ReservedInventoryItemId != nil {
			types[reservation.GetReservedInventoryItemId()] = struct{}{}
		}
	}
	return len(types)
}

// ApplyStealOnOwner runs on the owner's Actor mailbox: it validates
// player.CanSteal, atomically increments steal_count/stolen_quantity by the
// plot's frozen steal_quantity, and durably (synchronous SaveCAS) appends an
// OWNER FriendInteractionReceipt whose result_payload/result_digest_sha256
// answer every later retry for the same interactionID identically. A
// deterministic rejection (ErrStealNotAvailable) never touches owner state:
// no checkpoint_revision bump, no receipt, nothing to flush.
//
// farm_view_seq is only bumped, and FarmViewPatch only built and broadcast,
// once the owner checkpoint has durably committed the mutation, and exactly
// once per interaction: the attempt that proves durability owns the
// broadcast. So a retry after a failed SaveCAS returns alreadyApplied=true
// *and* the patch it now persisted, while a replay of an already-durable
// apply returns farmPatch == nil because the earlier attempt broadcast it.
// A retry that still cannot commit the owner mutation returns an error, never
// alreadyApplied=true.
func (r *Runtime) ApplyStealOnOwner(
	ctx context.Context,
	ownerID, ownerEpoch, visitorID uint64,
	interactionID []byte,
	plotID uint32,
) (resultPayload []byte, resultDigest []byte, farmPatch *wsv1.FarmViewPatch, alreadyApplied bool, err error) {
	_ = visitorID // identifies the caller for audit only; CanSteal is visitor-agnostic.
	if ownerEpoch == 0 {
		return nil, nil, nil, false, ErrNotOwner
	}
	if len(interactionID) != 16 || plotID == 0 {
		return nil, nil, nil, false, errors.New("invalid steal apply request")
	}
	shardID := routing.ShardForPlayer(ownerID)
	r.shardLocks[shardID].RLock()
	defer r.shardLocks[shardID].RUnlock()
	a, err := r.actorFor(ctx, ownerID, ownerEpoch)
	if err != nil {
		return nil, nil, nil, false, err
	}
	now := r.now().UTC()
	stepKey := syncStepKey(syncStepApplyStealOnOwner, interactionID)

	var mutated bool
	var stealErr error
	if execErr := a.mailbox.Do(ctx, func() {
		if existing := findFriendReceipt(
			a.state.FriendReceipts, interactionID, datav1.FriendReceiptRole_FRIEND_RECEIPT_OWNER,
		); existing != nil {
			resultPayload = append([]byte(nil), existing.ResultPayload...)
			resultDigest = append([]byte(nil), existing.ResultDigestSha256...)
			alreadyApplied = true
			return
		}
		migrateFriendSchema(a.state, now)
		plot := a.state.Plots[plotID]
		if !CanSteal(plot) {
			stealErr = ErrStealNotAvailable
			return
		}
		plot.StealCount++
		plot.StolenQuantity += plot.StealQuantity

		config := r.config.Load()
		guard := r.evaluateStealGuard(a.state, config, now.UnixMilli())
		payload := &wsv1.FriendActionResponse{
			InteractionId: append([]byte(nil), interactionID...),
			StealGuard:    guard,
		}
		body, marshalErr := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
		if marshalErr != nil {
			stealErr = fmt.Errorf("marshal steal apply result: %w", marshalErr)
			return
		}
		digest := sha256.Sum256(body)
		a.state.FriendReceipts = append(a.state.FriendReceipts, &datav1.FriendInteractionReceipt{
			InteractionId:      append([]byte(nil), interactionID...),
			Role:               datav1.FriendReceiptRole_FRIEND_RECEIPT_OWNER,
			Action:             datav1.FriendInteractionAction_STEAL_FRIEND_CROP,
			Status:             datav1.FriendReceiptStatus_FRIEND_RECEIPT_APPLIED,
			ResultDigestSha256: append([]byte(nil), digest[:]...),
			ResultPayload:      body,
			CommittedAtMs:      now.UnixMilli(),
		})
		a.state.PlayerSeq++
		a.state.CheckpointRevision++
		a.state.UpdatedAtMS = now.UnixMilli()
		mutated = true
		a.markSyncPending(stepKey, pendingSyncStep{
			revision:        a.state.CheckpointRevision,
			domainChanges: DomainChanges{}.PlotChanged(plotID),
		})
		resultPayload = body
		resultDigest = digest[:]
	}); execErr != nil {
		return nil, nil, nil, false, fmt.Errorf("execute apply steal mailbox: %w", execErr)
	}
	if stealErr != nil {
		return nil, nil, nil, false, stealErr
	}
	if !alreadyApplied && !mutated {
		return nil, nil, nil, false, errors.New("apply steal did not mutate owner state")
	}
	owedChanges, err := r.settleSyncStepLocked(ctx, ownerID, a, stepKey)
	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("flush steal apply: %w", err)
	}
	if !owedChanges.Empty() {
		farmPatch = r.publishFarmViewChanges(ctx, a, ownerID, owedChanges)
	}
	return resultPayload, resultDigest, farmPatch, alreadyApplied, nil
}

// CommitSteal runs on the visitor's Actor mailbox: it consumes the matching
// RESERVED reservation, credits cropItemID into the visitor's inventory,
// advances TASK_STEAL_CROP when the second chapter is IN_PROGRESS, and
// durably (synchronous SaveCAS) appends a VISITOR FriendInteractionReceipt
// carrying the final FriendActionResponse so a retry replays the same
// PlayerStatePatch — but only once that receipt is durable: a retry after a
// failed SaveCAS re-attempts the write instead of reporting the in-memory
// receipt as committed. ownerResultPayload (the OWNER receipt's result_payload,
// forwarded unmodified by the interaction Saga) is only used to recover
// owner_route's FarmViewPatch for the visitor's own immediate response; the
// visitor also receives that patch again as an ordinary FARM_VIEW_CHANGED
// push, so callers must dedupe on farm_view_seq rather than double-apply.
func (r *Runtime) CommitSteal(
	ctx context.Context,
	visitorID, ownerEpoch uint64,
	interactionID []byte,
	cropItemID, quantity uint32,
	ownerResultPayload []byte,
) (response *wsv1.FriendActionResponse, alreadyCommitted bool, err error) {
	if ownerEpoch == 0 {
		return nil, false, ErrNotOwner
	}
	if len(interactionID) != 16 || cropItemID == 0 || quantity == 0 {
		return nil, false, errors.New("invalid steal commit request")
	}
	shardID := routing.ShardForPlayer(visitorID)
	r.shardLocks[shardID].RLock()
	defer r.shardLocks[shardID].RUnlock()
	a, err := r.actorFor(ctx, visitorID, ownerEpoch)
	if err != nil {
		return nil, false, err
	}
	now := r.now().UTC()
	stepKey := syncStepKey(syncStepCommitSteal, interactionID)

	var mutated bool
	var commitErr error
	if execErr := a.mailbox.Do(ctx, func() {
		if existing := findFriendReceipt(
			a.state.FriendReceipts, interactionID, datav1.FriendReceiptRole_FRIEND_RECEIPT_VISITOR,
		); existing != nil {
			stored := &wsv1.FriendActionResponse{}
			if proto.Unmarshal(existing.ResultPayload, stored) != nil {
				commitErr = errors.New("stored steal commit receipt is corrupt")
				return
			}
			response = stored
			alreadyCommitted = true
			return
		}
		migrateFriendSchema(a.state, now)
		reservationIndex := -1
		for index, reservation := range a.state.FriendReservations {
			if bytes.Equal(reservation.InteractionId, interactionID) {
				reservationIndex = index
				break
			}
		}
		if reservationIndex < 0 {
			commitErr = ErrStealReservationMissing
			return
		}
		reservation := a.state.FriendReservations[reservationIndex]
		if reservation.Status != datav1.FriendReservationStatus_FRIEND_RESERVATION_RESERVED ||
			reservation.Action != datav1.FriendInteractionAction_STEAL_FRIEND_CROP ||
			reservation.GetReservedInventoryItemId() != cropItemID ||
			reservation.GetReservedInventoryQuantity() != quantity {
			commitErr = ErrStealReservationMissing
			return
		}
		reservation.Status = datav1.FriendReservationStatus_FRIEND_RESERVATION_CONSUMED
		reservation.UpdatedAtMs = now.UnixMilli()

		if a.state.Inventory == nil {
			a.state.Inventory = make(map[uint32]uint32)
		}
		newQuantity := a.state.Inventory[cropItemID] + quantity
		a.state.Inventory[cropItemID] = newQuantity
		if err := addStolenCareer(a.state, quantity); err != nil {
			commitErr = err
			return
		}
		incrementStealTask(a.state)

		var ownerFarmPatch *wsv1.FarmViewPatch
		var stealGuard *wsv1.StealGuardOutcome
		if len(ownerResultPayload) > 0 {
			ownerResponse := &wsv1.FriendActionResponse{}
			if proto.Unmarshal(ownerResultPayload, ownerResponse) == nil {
				ownerFarmPatch = ownerResponse.FarmPatch
				stealGuard = ownerResponse.StealGuard
			}
		}
		var coinBalance *int64
		appliedPenalty := int64(0)
		if stealGuard != nil && stealGuard.GuardTriggered && stealGuard.GuardPenaltyConfigured > 0 {
			appliedPenalty = stealGuard.GuardPenaltyConfigured
			if a.state.Coins < appliedPenalty {
				appliedPenalty = a.state.Coins
			}
			a.state.Coins -= appliedPenalty
			balance := a.state.Coins
			coinBalance = &balance
			// 冻结实扣金额，重试读取 receipt 不再改金币。
			stealGuard = proto.Clone(stealGuard).(*wsv1.StealGuardOutcome)
			stealGuard.GuardPenaltyApplied = appliedPenalty
		}
		friendResponse := &wsv1.FriendActionResponse{
			InteractionId: append([]byte(nil), interactionID...),
			VisitorPatch: &wsv1.PlayerStatePatch{
				CoinBalance:      coinBalance,
				InventoryUpserts: []*wsv1.ItemStackView{{ItemId: cropItemID, Quantity: newQuantity}},
				CurrentChapter:   a.state.Snapshot().CurrentChapter,
				Career:           careerView(a.state),
			},
			FarmPatch:  ownerFarmPatch,
			StealGuard: stealGuard,
		}
		body, marshalErr := proto.MarshalOptions{Deterministic: true}.Marshal(friendResponse)
		if marshalErr != nil {
			commitErr = fmt.Errorf("marshal steal commit result: %w", marshalErr)
			return
		}
		digest := sha256.Sum256(body)
		a.state.FriendReceipts = append(a.state.FriendReceipts, &datav1.FriendInteractionReceipt{
			InteractionId:      append([]byte(nil), interactionID...),
			Role:               datav1.FriendReceiptRole_FRIEND_RECEIPT_VISITOR,
			Action:             datav1.FriendInteractionAction_STEAL_FRIEND_CROP,
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
		return nil, false, fmt.Errorf("execute commit steal mailbox: %w", execErr)
	}
	if commitErr != nil {
		return nil, false, commitErr
	}
	if !alreadyCommitted && !mutated {
		return nil, false, errors.New("commit steal did not mutate visitor state")
	}
	if _, err := r.settleSyncStepLocked(ctx, visitorID, a, stepKey); err != nil {
		return nil, false, fmt.Errorf("flush steal commit: %w", err)
	}
	return response, alreadyCommitted, nil
}

// incrementStealTask mirrors incrementAddFriendTask for TASK_STEAL_CROP: a
// no-op once the chapter containing it has left IN_PROGRESS or has no
// TASK_STEAL_CROP entry (e.g. the visitor is still on chapter 1).
func incrementStealTask(state *State) {
	if state.Chapter != chapterv1.ChapterStatus_IN_PROGRESS {
		return
	}
	found := false
	for i := range state.Tasks {
		if state.Tasks[i].ID != stealTaskID {
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

// ReleaseSteal runs on the visitor's Actor mailbox and durably releases a
// live RESERVED reservation for interactionID, for the deterministic
// owner-rejection path (STEAL_NOT_AVAILABLE) before OWNER_APPLIED. It is
// idempotent: a missing reservation, or one already CONSUMED/RELEASED, is a
// no-op — except for a release this process performed but could not persist,
// which is re-flushed so the abort path cannot leave the visitor holding
// reserved capacity the store never released. Like ReserveSteal, only
// checkpoint_revision advances.
func (r *Runtime) ReleaseSteal(
	ctx context.Context,
	visitorID, ownerEpoch uint64,
	interactionID []byte,
) error {
	if ownerEpoch == 0 {
		return ErrNotOwner
	}
	if len(interactionID) != 16 {
		return errors.New("invalid steal release request")
	}
	shardID := routing.ShardForPlayer(visitorID)
	r.shardLocks[shardID].RLock()
	defer r.shardLocks[shardID].RUnlock()
	a, err := r.actorFor(ctx, visitorID, ownerEpoch)
	if err != nil {
		return err
	}
	now := r.now().UTC()
	stepKey := syncStepKey(syncStepReleaseSteal, interactionID)
	if execErr := a.mailbox.Do(ctx, func() {
		for _, reservation := range a.state.FriendReservations {
			if !bytes.Equal(reservation.InteractionId, interactionID) {
				continue
			}
			if reservation.Status != datav1.FriendReservationStatus_FRIEND_RESERVATION_RESERVED {
				return
			}
			reservation.Status = datav1.FriendReservationStatus_FRIEND_RESERVATION_RELEASED
			reservation.UpdatedAtMs = now.UnixMilli()
			a.state.CheckpointRevision++
			a.state.UpdatedAtMS = now.UnixMilli()
			a.markSyncPending(stepKey, pendingSyncStep{revision: a.state.CheckpointRevision})
			return
		}
	}); execErr != nil {
		return fmt.Errorf("execute release steal mailbox: %w", execErr)
	}
	if _, err := r.settleSyncStepLocked(ctx, visitorID, a, stepKey); err != nil {
		return fmt.Errorf("flush steal release: %w", err)
	}
	return nil
}

func findFriendReceipt(
	receipts []*datav1.FriendInteractionReceipt, interactionID []byte, role datav1.FriendReceiptRole,
) *datav1.FriendInteractionReceipt {
	for _, receipt := range receipts {
		if receipt.Role == role && bytes.Equal(receipt.InteractionId, interactionID) {
			return receipt
		}
	}
	return nil
}

func uint32Ptr(value uint32) *uint32 {
	return &value
}
