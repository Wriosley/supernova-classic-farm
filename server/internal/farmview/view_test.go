package farmview

import (
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
)

func TestPublicPlotViewStripsPrivateFieldsAndComputesHarvestable(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	mature := int64(1700000500000)
	plot := &player.Plot{
		ID:                  3,
		State:               plotv1.PlotState_MATURE,
		CropID:              1001,
		BaseYield:           10,
		StolenQuantity:      4,
		EstimatedMatureAtMS: &mature,
	}
	view := PublicPlotView(plot, now)
	if view.GetPlotId() != 3 || view.GetPlotState() != plotv1.PlotState_MATURE ||
		view.GetCropId() != 1001 || view.GetHarvestableQuantity() != 6 ||
		view.GetPestActive() || view.GetCanSteal() {
		t.Fatalf("view = %+v", view)
	}
	// EstimatedMatureAtMs is only meaningful while growing; MATURE plots keep
	// whatever was last recorded, matching Plot.View()'s private projection.
	if view.GetEstimatedMatureAtMs() != mature {
		t.Fatalf("estimated_mature_at_ms = %d, want %d", view.GetEstimatedMatureAtMs(), mature)
	}
}

func TestPublicPlotViewPestActiveWhenEffectPresent(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	plot := &player.Plot{
		ID:     1,
		State:  plotv1.PlotState_GROWING,
		CropID: 1001,
		PestEffect: &datav1.TimedEffectRecord{
			EffectItemOrPestId: 9001,
			StartAtMs:          1,
			EndAtMs:            2,
		},
	}
	view := PublicPlotView(plot, now)
	if !view.GetPestActive() {
		t.Fatalf("pest_active = false, want true")
	}
	if view.GetHarvestableQuantity() != 0 {
		t.Fatalf("harvestable_quantity = %d, want 0 for a growing plot", view.GetHarvestableQuantity())
	}
}

func TestPublicPlotViewHandlesNilPlot(t *testing.T) {
	if got := PublicPlotView(nil, time.Now()); got != nil {
		t.Fatalf("PublicPlotView(nil) = %+v, want nil", got)
	}
}

func TestSnapshotSortsByPlotIDAndStripsPrivateFields(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	plots := map[uint32]*player.Plot{
		3: {ID: 3, State: plotv1.PlotState_EMPTY},
		1: {ID: 1, State: plotv1.PlotState_MATURE, BaseYield: 5},
		2: {ID: 2, State: plotv1.PlotState_GROWING, CropID: 42},
	}
	epoch := []byte("0123456789abcdef")
	snapshot := Snapshot(77, epoch, 9, plots, now)
	if snapshot.GetOwnerPlayerId() != 77 {
		t.Fatalf("owner_player_id = %d, want 77", snapshot.GetOwnerPlayerId())
	}
	if snapshot.GetVersion().GetFarmViewSeq() != 9 ||
		string(snapshot.GetVersion().GetFarmViewEpoch()) != string(epoch) {
		t.Fatalf("version = %+v", snapshot.GetVersion())
	}
	if len(snapshot.GetPlots()) != 3 {
		t.Fatalf("plots = %d, want 3", len(snapshot.GetPlots()))
	}
	for i, want := range []uint32{1, 2, 3} {
		if snapshot.GetPlots()[i].GetPlotId() != want {
			t.Fatalf("plots[%d].plot_id = %d, want %d", i, snapshot.GetPlots()[i].GetPlotId(), want)
		}
	}
}
