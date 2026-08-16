package player

import (
	"testing"

	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
)

func TestComputeFarmQuickSummaryUsesEarliestGrowingAndMatureCandidate(t *testing.T) {
	early, late := int64(200), int64(500)
	state := &State{PlayerID: 7, OwnerEpoch: 2, CheckpointRevision: 9, Plots: map[uint32]*Plot{
		1: {ID: 1, State: plotv1.PlotState_GROWING, EstimatedMatureAtMS: &late},
		2: {ID: 2, State: plotv1.PlotState_GROWING, EstimatedMatureAtMS: &early},
		3: {ID: 3, State: plotv1.PlotState_MATURE, CropItemID: 1001, BaseYield: 4, StealQuantity: 1, MaxStealTimes: 1, ProtectedOwnerYield: 2},
	}}
	got := computeFarmQuickSummary(state)
	if !got.HasGrowingCrop || got.EarliestMatureAtMS != early || !got.HasMatureCropCandidate {
		t.Fatalf("summary=%+v", got)
	}
}
