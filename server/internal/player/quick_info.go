package player

import (
	"context"

	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
)

type FarmQuickSummary struct {
	PlayerID               uint64
	OwnerEpoch             uint64
	CheckpointRevision     uint64
	HasGrowingCrop         bool
	EarliestMatureAtMS     int64
	HasMatureCropCandidate bool
	UpdatedAtMS            int64
}

type FarmQuickInfoNotifier interface {
	NotifyFarmQuickInfo(context.Context, FarmQuickSummary)
}

func computeFarmQuickSummary(state *State) FarmQuickSummary {
	if state == nil {
		return FarmQuickSummary{}
	}
	summary := FarmQuickSummary{PlayerID: state.PlayerID, OwnerEpoch: state.OwnerEpoch, CheckpointRevision: state.CheckpointRevision, UpdatedAtMS: state.UpdatedAtMS}
	for _, plot := range state.Plots {
		if plot == nil {
			continue
		}
		if plot.State == plotv1.PlotState_GROWING {
			summary.HasGrowingCrop = true
			if plot.EstimatedMatureAtMS != nil && (summary.EarliestMatureAtMS == 0 || *plot.EstimatedMatureAtMS < summary.EarliestMatureAtMS) {
				summary.EarliestMatureAtMS = *plot.EstimatedMatureAtMS
			}
		}
		if plot.State == plotv1.PlotState_MATURE && CanSteal(plot) {
			summary.HasMatureCropCandidate = true
		}
	}
	return summary
}

func (r *Runtime) publishFarmQuickSummaryValue(a *runtimeActor, summary FarmQuickSummary) {
	if r == nil || a == nil || summary.PlayerID == 0 {
		return
	}
	previous := a.quickSummary.Load()
	if previous != nil && *previous == summary {
		return
	}
	copy := summary
	a.quickSummary.Store(&copy)
	r.mu.Lock()
	notifier := r.quickInfo
	r.mu.Unlock()
	if notifier != nil {
		notifier.NotifyFarmQuickInfo(r.backgroundCtx, summary)
	}
}

func (r *Runtime) publishFarmQuickSummary(a *runtimeActor) {
	if r == nil || a == nil {
		return
	}
	var summary FarmQuickSummary
	if err := a.mailbox.Do(r.backgroundCtx, func() { summary = computeFarmQuickSummary(a.state) }); err == nil {
		r.publishFarmQuickSummaryValue(a, summary)
	}
}

func (r *Runtime) SetFarmQuickInfoNotifier(notifier FarmQuickInfoNotifier) {
	r.mu.Lock()
	r.quickInfo = notifier
	r.mu.Unlock()
}
