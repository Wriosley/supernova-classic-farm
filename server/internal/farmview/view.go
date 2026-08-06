// Package farmview projects a farm owner's private Player Actor state into
// the public, visitor-safe FarmVisitSnapshot: no coins, inventory, tasks or
// friend-action chances ever cross this boundary.
package farmview

import (
	"sort"
	"time"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
)

// PublicPlotView projects one owner Plot into its public wire view: plot ID,
// state, crop, estimated maturity, harvestable quantity, whether a pest is
// currently active and steal eligibility (player.CanSteal, Phase 5).
func PublicPlotView(plot *player.Plot, _ time.Time) *wsv1.PublicPlotView {
	if plot == nil {
		return nil
	}
	view := &wsv1.PublicPlotView{
		PlotId:     plot.ID,
		PlotState:  plot.State,
		CropId:     plot.CropID,
		PestActive: plot.PestEffect != nil,
		CanSteal:   player.CanSteal(plot),
	}
	if plot.EstimatedMatureAtMS != nil {
		view.EstimatedMatureAtMs = *plot.EstimatedMatureAtMS
	}
	if plot.State == plotv1.PlotState_MATURE && plot.BaseYield >= plot.StolenQuantity {
		view.HarvestableQuantity = plot.BaseYield - plot.StolenQuantity
	}
	return view
}

// Snapshot builds one FarmVisitSnapshot from the owner's live plot map,
// sorted by plot_id like PlayerSnapshot.Plots. epoch and seq are supplied by
// the caller (see visit.OwnerService), which owns the farm_view_epoch/seq
// lifecycle; this package only performs the plot projection itself.
func Snapshot(
	ownerPlayerID uint64,
	epoch []byte,
	seq uint64,
	plots map[uint32]*player.Plot,
	now time.Time,
) *wsv1.FarmVisitSnapshot {
	plotIDs := make([]uint32, 0, len(plots))
	for plotID := range plots {
		plotIDs = append(plotIDs, plotID)
	}
	sort.Slice(plotIDs, func(i, j int) bool { return plotIDs[i] < plotIDs[j] })
	views := make([]*wsv1.PublicPlotView, 0, len(plotIDs))
	for _, plotID := range plotIDs {
		views = append(views, PublicPlotView(plots[plotID], now))
	}
	return &wsv1.FarmVisitSnapshot{
		OwnerPlayerId: ownerPlayerID,
		Version: &wsv1.FarmViewVersion{
			FarmViewEpoch: append([]byte(nil), epoch...),
			FarmViewSeq:   seq,
		},
		Plots: views,
	}
}
