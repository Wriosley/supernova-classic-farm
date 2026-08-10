package player

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/protobuf/proto"
)

func (r *Runtime) ApplyApplyPestOnOwner(
	ctx context.Context, ownerID, ownerEpoch, visitorID uint64,
	interactionID []byte, plotID, pestID uint32,
) (resultPayload, resultDigest []byte, farmPatch *wsv1.FarmViewPatch, alreadyApplied bool, err error) {
	if ownerEpoch == 0 {
		return nil, nil, nil, false, ErrNotOwner
	}
	if len(interactionID) != 16 || plotID == 0 || pestID == 0 {
		return nil, nil, nil, false, errors.New("invalid apply pest request")
	}
	pest, ok := r.CurrentConfig().Pest(pestID)
	if !ok || !pest.Enabled || pest.DurationMS <= 0 || pest.ModifierScaled6 >= 0 {
		return nil, nil, nil, false, errors.New("pest config is unavailable")
	}
	shardID := routing.ShardForPlayer(ownerID)
	r.shardLocks[shardID].RLock()
	defer r.shardLocks[shardID].RUnlock()
	a, err := r.actorFor(ctx, ownerID, ownerEpoch)
	if err != nil {
		return nil, nil, nil, false, err
	}
	now := r.now().UTC()
	stepKey := syncStepKey(syncStepApplyPestOnOwner, interactionID)
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
		if plot == nil || plot.State != plotv1.PlotState_GROWING {
			applyErr = ErrPlotNotEligible
			return
		}
		if plot.PestEffect != nil {
			applyErr = ErrPestAlreadyPresent
			return
		}
		if now.UnixMilli() > math.MaxInt64-pest.DurationMS {
			applyErr = errors.New("pest duration overflows")
			return
		}
		before := clonePlot(plot)
		matured, settleErr := settleGrowingPlot(plot, now.UnixMilli())
		if settleErr != nil || matured {
			*plot = *before
			applyErr = ErrPlotNotEligible
			return
		}
		plot.PestEffect = &datav1.TimedEffectRecord{
			EffectInstanceId: pestEffectID(visitorID, interactionID),
			EffectKind:       datav1.EffectKind_PEST, EffectItemOrPestId: pestID,
			ConfigVersion: pest.ConfigVersion,
			Modifier:      &datav1.RateDecimal6{ScaledValue: pest.ModifierScaled6},
			StartAtMs:     now.UnixMilli(), EndAtMs: now.UnixMilli() + pest.DurationMS,
			SourcePlayerId: uint64Ptr(visitorID),
		}
		estimate, estimateErr := estimatePlotMatureAtMS(plot, now.UnixMilli())
		if estimateErr != nil {
			*plot = *before
			applyErr = estimateErr
			return
		}
		plot.EstimatedMatureAtMS = &estimate
		migrateFriendSchema(a.state, now)
		resultPayload, resultDigest, applyErr = appendOwnerActionReceipt(
			a.state, interactionID, datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND, now.UnixMilli(),
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
		return nil, nil, nil, false, fmt.Errorf("execute apply pest mailbox: %w", err)
	}
	if applyErr != nil {
		return nil, nil, nil, false, applyErr
	}
	if !alreadyApplied && !mutated {
		return nil, nil, nil, false, errors.New("apply pest did not mutate owner state")
	}
	owedChanges, err := r.settleSyncStepLocked(ctx, ownerID, a, stepKey)
	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("flush apply pest: %w", err)
	}
	if !owedChanges.Empty() {
		farmPatch = r.publishFarmViewChanges(ctx, a, ownerID, owedChanges)
	}
	return resultPayload, resultDigest, farmPatch, alreadyApplied, nil
}

func appendOwnerActionReceipt(
	state *State, interactionID []byte, action datav1.FriendInteractionAction, nowMS int64,
) ([]byte, []byte, error) {
	payload := &wsv1.FriendActionResponse{InteractionId: append([]byte(nil), interactionID...)}
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	digest := sha256.Sum256(body)
	state.FriendReceipts = append(state.FriendReceipts, &datav1.FriendInteractionReceipt{
		InteractionId: append([]byte(nil), interactionID...),
		Role:          datav1.FriendReceiptRole_FRIEND_RECEIPT_OWNER, Action: action,
		Status:             datav1.FriendReceiptStatus_FRIEND_RECEIPT_APPLIED,
		ResultDigestSha256: digest[:], ResultPayload: body, CommittedAtMs: nowMS,
	})
	return body, digest[:], nil
}

func pestEffectID(playerID uint64, interactionID []byte) []byte {
	body := append([]byte("pest-effect:"), binary.BigEndian.AppendUint64(nil, playerID)...)
	body = append(body, interactionID...)
	sum := sha256.Sum256(body)
	id := append([]byte(nil), sum[:16]...)
	id[6] = (id[6] & 0x0f) | 0x50
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}

func uint64Ptr(value uint64) *uint64 { return &value }
