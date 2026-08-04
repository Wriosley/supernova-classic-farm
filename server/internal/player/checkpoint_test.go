package player

import (
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
)

func TestInitialCheckpointRoundTrip(t *testing.T) {
	now := time.Date(2026, 7, 31, 6, 30, 0, 0, time.UTC)
	checkpoint := NewInitialCheckpoint(42, now)
	body, digest, err := MarshalCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalCheckpoint(body, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	state, err := StateFromCheckpoint(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if state.PlayerID != 42 ||
		state.PlayerSeq != 0 ||
		state.Coins != InitialCoinBalance ||
		state.Inventory[BasicFertilizerID] != 1 ||
		len(state.Plots) != int(InitialPlotCount) ||
		len(state.Tasks) != 5 {
		t.Fatalf("unexpected restored state: %+v", state)
	}
	for plotID := InitialPlotID; plotID < InitialPlotID+InitialPlotCount; plotID++ {
		if plot := state.Plots[plotID]; plot == nil || plot.State != plotv1.PlotState_EMPTY {
			t.Fatalf("initial plot %d is invalid: %+v", plotID, plot)
		}
	}
}

func TestCheckpointRejectsDigestAndShardMismatch(t *testing.T) {
	checkpoint := NewInitialCheckpoint(42, time.Now())
	body, digest, err := MarshalCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	digest[0] ^= 0xff
	if _, err := UnmarshalCheckpoint(body, digest[:]); err == nil {
		t.Fatal("digest mismatch was accepted")
	}

	checkpoint.LogicalShardId = (checkpoint.LogicalShardId + 1) % 4096
	if checkpoint.LogicalShardId == 0 {
		checkpoint.LogicalShardId = 1
	}
	if _, _, err := MarshalCheckpoint(checkpoint); err == nil {
		t.Fatal("shard mismatch was accepted")
	}
}

func TestCheckpointRejectsDuplicateInventory(t *testing.T) {
	checkpoint := NewInitialCheckpoint(42, time.Now())
	checkpoint.Inventory = append(checkpoint.Inventory, &datav1.InventoryStack{
		ItemId: BasicFertilizerID, Quantity: 2,
	})
	if _, err := StateFromCheckpoint(checkpoint); err == nil {
		t.Fatal("duplicate inventory was accepted")
	}
}

func TestGrowingPlotCheckpointRoundTrip(t *testing.T) {
	now := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	state, err := StateFromCheckpoint(NewInitialCheckpoint(42, now))
	if err != nil {
		t.Fatal(err)
	}
	estimate := now.Add(100 * time.Second).UnixMilli()
	state.Plots[InitialPlotID] = &Plot{
		ID: InitialPlotID, State: plotv1.PlotState_GROWING,
		CropID: 2001, CropItemID: 1002, CropConfigVersion: 1,
		PlantedAtMS: now.UnixMilli(), MaturityValueScaled9: 100_000_000_000,
		BaseGrowthRateScaled6: 1_000_000, BaseYield: 3,
		LastSettledAtMS: now.UnixMilli(), EstimatedMatureAtMS: &estimate,
	}
	state.PlayerSeq = 1
	state.CheckpointRevision = 2
	checkpoint, err := state.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	body, digest, err := MarshalCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalCheckpoint(body, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	restored, err := StateFromCheckpoint(decoded)
	if err != nil {
		t.Fatal(err)
	}
	plot := restored.Plots[InitialPlotID]
	if plot.State != plotv1.PlotState_GROWING || plot.CropID != 2001 ||
		plot.EstimatedMatureAtMS == nil || *plot.EstimatedMatureAtMS != estimate {
		t.Fatalf("unexpected restored growing plot: %+v", plot)
	}
}
