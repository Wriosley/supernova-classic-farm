package player

import (
	"testing"
	"time"

	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
	"github.com/Wriosley/supernova-classic-farm/server/internal/actor"
)

func TestRuntimeActorNextTickAtChoosesEarliestPlot(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	early := now.Add(30 * time.Second).UnixMilli()
	late := now.Add(90 * time.Second).UnixMilli()
	a := &runtimeActor{
		mailbox: actor.NewMailbox(1),
		state: &State{
			PlayerID: 7,
			Plots: map[uint32]*Plot{
				1: {
					ID: 1, State: plotv1.PlotState_GROWING,
					EstimatedMatureAtMS: &late,
				},
				2: {
					ID: 2, State: plotv1.PlotState_GROWING,
					EstimatedMatureAtMS: &early,
				},
				3: {
					ID: 3, State: plotv1.PlotState_MATURE,
				},
			},
		},
	}
	defer a.mailbox.Close()

	deadline, ok := a.nextTickAt()
	if !ok {
		t.Fatal("expected a deadline from growing plots")
	}
	if !deadline.Equal(now.Add(30 * time.Second)) {
		t.Fatalf("nextTickAt = %v, want earliest plot at +30s", deadline)
	}

	a.state.Plots = map[uint32]*Plot{
		1: {ID: 1, State: plotv1.PlotState_EMPTY},
		2: {ID: 2, State: plotv1.PlotState_MATURE},
	}
	if _, ok := a.nextTickAt(); ok {
		t.Fatal("expected no deadline when nothing is growing")
	}
}

func TestRuntimeActorTickMaterializesDuePlotsAndReturnsNextDeadline(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	due := now.UnixMilli()
	later := now.Add(2 * time.Minute).UnixMilli()
	a := &runtimeActor{
		mailbox: actor.NewMailbox(1),
		state: &State{
			PlayerID:           8,
			OwnerEpoch:         LocalOwnerEpoch,
			PlayerSeq:          1,
			CheckpointRevision: 1,
			Plots: map[uint32]*Plot{
				1: {
					ID: 1, State: plotv1.PlotState_GROWING,
					CropID: 2001, CropItemID: 1002, CropConfigVersion: 1,
					PlantedAtMS: now.Add(-100 * time.Second).UnixMilli(),
					MaturityValueScaled9: 100_000_000_000, BaseGrowthRateScaled6: 1_000_000,
					BaseYield: 3, LastSettledAtMS: now.Add(-100 * time.Second).UnixMilli(),
					SettledGrowthValueScaled9: 0, EstimatedMatureAtMS: &due,
				},
				2: {
					ID: 2, State: plotv1.PlotState_GROWING,
					CropID: 2001, CropItemID: 1002, CropConfigVersion: 1,
					PlantedAtMS: now.UnixMilli(),
					MaturityValueScaled9: 100_000_000_000, BaseGrowthRateScaled6: 1_000_000,
					BaseYield: 3, LastSettledAtMS: now.UnixMilli(),
					EstimatedMatureAtMS: &later,
				},
			},
		},
	}
	defer a.mailbox.Close()

	result, err := a.tick(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.MaturityEvents) != 1 || result.MaturityEvents[0].Plot.GetPlotId() != 1 {
		t.Fatalf("maturity events = %+v", result.MaturityEvents)
	}
	if result.DirtyRevision != 2 {
		t.Fatalf("DirtyRevision = %d, want 2", result.DirtyRevision)
	}
	if result.DomainChanges.Empty() || len(result.DomainChanges.PlotIDs()) != 1 ||
		result.DomainChanges.PlotIDs()[0] != 1 {
		t.Fatalf("DomainChanges = %+v", result.DomainChanges.PlotIDs())
	}
	if result.NextTickAt == nil || !result.NextTickAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("NextTickAt = %v, want remaining growing plot", result.NextTickAt)
	}
	if a.state.Plots[1].State != plotv1.PlotState_MATURE {
		t.Fatalf("plot 1 state = %v, want MATURE", a.state.Plots[1].State)
	}
}
