package player

import (
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
)

// CanSteal reports whether a visitor may currently steal one frozen
// steal_quantity unit of crop from plot, per
// docs/contracts/data-model.md §18 and
// docs/plans/friend_design_plan/03-好友互动Saga详细设计.md §3:
//
//   - the plot must be MATURE;
//   - steal_quantity and max_steal_times must both be frozen positive
//     (a plot planted before Phase 5, or under a CropConfig with no steal
//     configuration, freezes both at zero and is never stealable);
//   - steal_count must not have already reached max_steal_times;
//   - the remaining harvestable yield after this steal must still leave the
//     owner at least protected_owner_yield: base_yield >= stolen_quantity +
//     steal_quantity + protected_owner_yield (checked without underflow).
func CanSteal(plot *Plot) bool {
	if plot == nil || plot.State != plotv1.PlotState_MATURE {
		return false
	}
	if plot.StealQuantity == 0 || plot.MaxStealTimes == 0 {
		return false
	}
	if plot.StealCount >= plot.MaxStealTimes {
		return false
	}
	required := uint64(plot.StolenQuantity) + uint64(plot.StealQuantity) + uint64(plot.ProtectedOwnerYield)
	return uint64(plot.BaseYield) >= required
}

// visitorAlreadyStole reports whether visitorID already stole from this crop round.
func visitorAlreadyStole(plot *Plot, visitorID uint64) bool {
	if plot == nil || visitorID == 0 {
		return false
	}
	for _, id := range plot.StealVisitorPlayerIDs {
		if id == visitorID {
			return true
		}
	}
	return false
}

func CanApplyPest(plot *Plot) bool {
	return plot != nil && plot.State == plotv1.PlotState_GROWING && plot.PestEffect == nil
}

func CanCatchPest(plot *Plot, catcherPlayerID uint64) bool {
	if plot == nil || plot.State != plotv1.PlotState_GROWING || plot.PestEffect == nil {
		return false
	}
	return plot.PestEffect.SourcePlayerId == nil ||
		plot.PestEffect.GetSourcePlayerId() != catcherPlayerID
}

func CanHelpClean(plot *Plot) bool {
	return plot != nil && plot.State == plotv1.PlotState_NEED_CLEANUP
}
