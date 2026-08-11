package player

import (
	"context"
	"crypto/rand"
	"fmt"
	"sort"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

// newFarmViewEpoch mints the 16-byte identity that changes whenever this
// process (re)activates a player's Actor: first touch, Zone restart (a fresh
// process has an empty Actor map), and post-migration reacquisition (drain
// evicts the Actor, so the next actorFor call takes the "created" path
// again). It intentionally lives alongside private player.State rather than
// in package farmview, which only ever projects an already-decided epoch.
func newFarmViewEpoch() ([]byte, error) {
	epoch := make([]byte, 16)
	if _, err := rand.Read(epoch); err != nil {
		return nil, err
	}
	return epoch, nil
}

// BuildPublicFarmSnapshot activates (or reuses) ownerPlayerID's Actor,
// materializes due maturities exactly like Handle does, and projects the
// current plot map into a visitor-safe FarmVisitSnapshot carrying that
// Actor incarnation's farm_view_epoch. farm_view_seq is always 0 in Phase 3;
// Phase 4 will increment it on plot mutations from inside the same mailbox.
func (r *Runtime) BuildPublicFarmSnapshot(
	ctx context.Context,
	ownerPlayerID uint64,
	ownerEpoch uint64,
) (*wsv1.FarmVisitSnapshot, error) {
	if ownerEpoch == 0 {
		return nil, ErrNotOwner
	}
	shardID := routing.ShardForPlayer(ownerPlayerID)
	r.shardLocks[shardID].RLock()
	defer r.shardLocks[shardID].RUnlock()
	serverNow := r.now()
	a, err := r.actorFor(ctx, ownerPlayerID, ownerEpoch)
	if err != nil {
		return nil, err
	}
	var snapshot *wsv1.FarmVisitSnapshot
	var dirty bool
	var dirtyRevision uint64
	var executionErr error
	var maturityEvents []MaturityEvent
	err = a.mailbox.Do(ctx, func() {
		maturityEvents, executionErr = a.state.materializeDueMaturities(serverNow)
		if executionErr != nil {
			return
		}
		if len(maturityEvents) > 0 {
			dirty = true
			dirtyRevision = a.state.CheckpointRevision
		}
		snapshot = publicFarmSnapshot(
			ownerPlayerID, a.farmViewEpoch, a.farmViewSeq, a.state.Plots, careerView(a.state),
		)
	})
	if err != nil {
		return nil, fmt.Errorf("execute player mailbox: %w", err)
	}
	if executionErr != nil {
		return nil, fmt.Errorf("materialize player maturity: %w", executionErr)
	}
	if dirty {
		r.markDirty(ownerPlayerID, dirtyRevision)
	}
	if len(maturityEvents) > 0 {
		_ = r.forwardMaturityEvents(ctx, maturityEvents)
	}
	return snapshot, nil
}

// publicFarmSnapshot mirrors package farmview's projection. It is
// intentionally duplicated (rather than imported) because farmview imports
// player for the *Plot type; player importing farmview back would be a
// circular dependency, so this ~15-line projection stays local to the
// mailbox-protected caller that owns the private Plot map.
func publicFarmSnapshot(
	ownerPlayerID uint64,
	farmViewEpoch []byte,
	farmViewSeq uint64,
	plots map[uint32]*Plot,
	career *wsv1.PlayerCareerView,
) *wsv1.FarmVisitSnapshot {
	plotIDs := make([]uint32, 0, len(plots))
	for plotID := range plots {
		plotIDs = append(plotIDs, plotID)
	}
	sort.Slice(plotIDs, func(i, j int) bool { return plotIDs[i] < plotIDs[j] })
	views := make([]*wsv1.PublicPlotView, 0, len(plotIDs))
	for _, plotID := range plotIDs {
		views = append(views, publicPlotView(plots[plotID]))
	}
	if career == nil {
		career = &wsv1.PlayerCareerView{}
	}
	return &wsv1.FarmVisitSnapshot{
		OwnerPlayerId: ownerPlayerID,
		Version: &wsv1.FarmViewVersion{
			FarmViewEpoch: append([]byte(nil), farmViewEpoch...),
			FarmViewSeq:   farmViewSeq,
		},
		Plots:  views,
		Career: career,
	}
}

// buildFarmViewPatch projects only plotIDs (deduplicated and sorted, mirroring
// publicFarmSnapshot's stable plot_id order) into a FarmViewPatch carrying
// the Actor incarnation's current farm_view_epoch and the seq already bumped
// by the caller. A plot ID with no current entry (e.g. removed by a future
// feature) is silently skipped rather than sending a nil view.
func buildFarmViewPatch(
	ownerPlayerID uint64,
	farmViewEpoch []byte,
	farmViewSeq uint64,
	plotIDs []uint32,
	plots map[uint32]*Plot,
) *wsv1.FarmViewPatch {
	seen := make(map[uint32]struct{}, len(plotIDs))
	uniqueIDs := make([]uint32, 0, len(plotIDs))
	for _, plotID := range plotIDs {
		if _, exists := seen[plotID]; exists {
			continue
		}
		seen[plotID] = struct{}{}
		uniqueIDs = append(uniqueIDs, plotID)
	}
	sort.Slice(uniqueIDs, func(i, j int) bool { return uniqueIDs[i] < uniqueIDs[j] })
	views := make([]*wsv1.PublicPlotView, 0, len(uniqueIDs))
	for _, plotID := range uniqueIDs {
		if plot, exists := plots[plotID]; exists {
			views = append(views, publicPlotView(plot))
		}
	}
	return &wsv1.FarmViewPatch{
		OwnerPlayerId: ownerPlayerID,
		Version: &wsv1.FarmViewVersion{
			FarmViewEpoch: append([]byte(nil), farmViewEpoch...),
			FarmViewSeq:   farmViewSeq,
		},
		PlotUpserts: views,
	}
}

func publicPlotView(plot *Plot) *wsv1.PublicPlotView {
	if plot == nil {
		return nil
	}
	view := &wsv1.PublicPlotView{
		PlotId:     plot.ID,
		PlotState:  plot.State,
		CropId:     plot.CropID,
		CropItemId: plot.CropItemID,
		PestActive: plot.PestEffect != nil,
		CanSteal:   CanSteal(plot),
		StealCount: plot.StealCount,
	}
	if plot.EstimatedMatureAtMS != nil {
		view.EstimatedMatureAtMs = *plot.EstimatedMatureAtMS
	}
	if plot.State == plotv1.PlotState_MATURE && plot.BaseYield >= plot.StolenQuantity {
		view.HarvestableQuantity = plot.BaseYield - plot.StolenQuantity
	}
	return view
}
