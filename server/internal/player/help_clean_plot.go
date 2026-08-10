package player

import (
	"context"
	"errors"
	"fmt"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func (r *Runtime) ApplyHelpCleanOnOwner(
	ctx context.Context, ownerID, ownerEpoch, visitorID uint64,
	interactionID []byte, plotID uint32,
) (resultPayload, resultDigest []byte, farmPatch *wsv1.FarmViewPatch, alreadyApplied bool, err error) {
	_ = visitorID
	if ownerEpoch == 0 {
		return nil, nil, nil, false, ErrNotOwner
	}
	if len(interactionID) != 16 || plotID == 0 {
		return nil, nil, nil, false, errors.New("invalid help clean request")
	}
	shardID := routing.ShardForPlayer(ownerID)
	r.shardLocks[shardID].RLock()
	defer r.shardLocks[shardID].RUnlock()
	a, err := r.actorFor(ctx, ownerID, ownerEpoch)
	if err != nil {
		return nil, nil, nil, false, err
	}
	now := r.now().UTC()
	stepKey := syncStepKey(syncStepHelpCleanOnOwner, interactionID)
	var mutated bool
	var applyErr error
	if err := a.mailbox.Do(ctx, func() {
		if existing := findFriendReceipt(a.state.FriendReceipts, interactionID, datav1.FriendReceiptRole_FRIEND_RECEIPT_OWNER); existing != nil {
			resultPayload = append([]byte(nil), existing.ResultPayload...)
			resultDigest = append([]byte(nil), existing.ResultDigestSha256...)
			alreadyApplied = true
			return
		}
		plot := a.state.Plots[plotID]
		if !CanHelpClean(plot) {
			applyErr = ErrPlotNotEligible
			return
		}
		*plot = Plot{ID: plotID, State: plotv1.PlotState_EMPTY}
		migrateFriendSchema(a.state, now)
		resultPayload, resultDigest, applyErr = appendOwnerActionReceipt(
			a.state, interactionID, datav1.FriendInteractionAction_HELP_CLEAN_FRIEND_PLOT, now.UnixMilli(),
		)
		if applyErr != nil {
			return
		}
		a.state.PlayerSeq++
		a.state.CheckpointRevision++
		a.state.UpdatedAtMS = now.UnixMilli()
		mutated = true
		a.markSyncPending(stepKey, pendingSyncStep{
			revision: a.state.CheckpointRevision, domainChanges: DomainChanges{}.PlotChanged(plotID),
		})
	}); err != nil {
		return nil, nil, nil, false, fmt.Errorf("execute help clean mailbox: %w", err)
	}
	if applyErr != nil {
		return nil, nil, nil, false, applyErr
	}
	if !alreadyApplied && !mutated {
		return nil, nil, nil, false, errors.New("help clean did not mutate owner state")
	}
	owedChanges, err := r.settleSyncStepLocked(ctx, ownerID, a, stepKey)
	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("flush help clean: %w", err)
	}
	if !owedChanges.Empty() {
		farmPatch = r.publishFarmViewChanges(ctx, a, ownerID, owedChanges)
	}
	return resultPayload, resultDigest, farmPatch, alreadyApplied, nil
}
