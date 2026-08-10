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

func (r *Runtime) ApplyCatchPestOnOwner(
	ctx context.Context, ownerID, ownerEpoch, visitorID uint64,
	interactionID []byte, plotID uint32,
) (resultPayload, resultDigest []byte, farmPatch *wsv1.FarmViewPatch, alreadyApplied bool, err error) {
	if ownerEpoch == 0 {
		return nil, nil, nil, false, ErrNotOwner
	}
	if len(interactionID) != 16 || plotID == 0 {
		return nil, nil, nil, false, errors.New("invalid catch pest request")
	}
	shardID := routing.ShardForPlayer(ownerID)
	r.shardLocks[shardID].RLock()
	defer r.shardLocks[shardID].RUnlock()
	a, err := r.actorFor(ctx, ownerID, ownerEpoch)
	if err != nil {
		return nil, nil, nil, false, err
	}
	now := r.now().UTC()
	stepKey := syncStepKey(syncStepCatchPestOnOwner, interactionID)
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
		if plot == nil || plot.State != plotv1.PlotState_GROWING || plot.PestEffect == nil {
			applyErr = ErrPlotNotEligible
			return
		}
		if plot.PestEffect.SourcePlayerId != nil && plot.PestEffect.GetSourcePlayerId() == visitorID {
			applyErr = ErrPestSourceForbidden
			return
		}
		before := clonePlot(plot)
		matured, settleErr := settleGrowingPlot(plot, now.UnixMilli())
		if settleErr != nil || matured || plot.PestEffect == nil {
			*plot = *before
			applyErr = ErrPlotNotEligible
			return
		}
		plot.PestEffect = nil
		estimate, estimateErr := estimatePlotMatureAtMS(plot, now.UnixMilli())
		if estimateErr != nil {
			*plot = *before
			applyErr = estimateErr
			return
		}
		plot.EstimatedMatureAtMS = &estimate
		migrateFriendSchema(a.state, now)
		resultPayload, resultDigest, applyErr = appendOwnerActionReceipt(
			a.state, interactionID, datav1.FriendInteractionAction_CATCH_PEST_FOR_FRIEND, now.UnixMilli(),
		)
		if applyErr != nil {
			*plot = *before
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
		return nil, nil, nil, false, fmt.Errorf("execute catch pest mailbox: %w", err)
	}
	if applyErr != nil {
		return nil, nil, nil, false, applyErr
	}
	if !alreadyApplied && !mutated {
		return nil, nil, nil, false, errors.New("catch pest did not mutate owner state")
	}
	owedChanges, err := r.settleSyncStepLocked(ctx, ownerID, a, stepKey)
	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("flush catch pest: %w", err)
	}
	if !owedChanges.Empty() {
		farmPatch = r.publishFarmViewChanges(ctx, a, ownerID, owedChanges)
	}
	return resultPayload, resultDigest, farmPatch, alreadyApplied, nil
}
