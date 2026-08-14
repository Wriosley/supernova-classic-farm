package player

import (
	"context"
	"math"
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
	reasonv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/reason"
)

type pushForwarderFunc func(context.Context, *wsv1.WsEnvelope) error

func (f pushForwarderFunc) Forward(ctx context.Context, envelope *wsv1.WsEnvelope) error {
	return f(ctx, envelope)
}

func growingPlotAt(now time.Time) *Plot {
	estimate := now.Add(100 * time.Second).UnixMilli()
	return &Plot{
		ID: 1, State: plotv1.PlotState_GROWING,
		CropID: 2001, CropItemID: 1002, CropConfigVersion: 1,
		PlantedAtMS: now.UnixMilli(), MaturityValueScaled9: 100_000_000_000,
		BaseGrowthRateScaled6: 1_000_000, BaseYield: 3,
		LastSettledAtMS: now.UnixMilli(), EstimatedMatureAtMS: &estimate,
	}
}

func TestSettleGrowingPlotUsesExactFixedPointAndMaterializesMaturity(t *testing.T) {
	now := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	plot := growingPlotAt(now)
	matured, err := settleGrowingPlot(plot, now.Add(50*time.Second).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if matured || plot.SettledGrowthValueScaled9 != 50_000_000_000 ||
		plot.EstimatedMatureAtMS == nil ||
		*plot.EstimatedMatureAtMS != now.Add(100*time.Second).UnixMilli() {
		t.Fatalf("unexpected half-grown plot: %+v", plot)
	}
	matured, err = settleGrowingPlot(plot, now.Add(100*time.Second).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if !matured || plot.State != plotv1.PlotState_MATURE ||
		plot.SettledGrowthValueScaled9 != plot.MaturityValueScaled9 ||
		plot.EstimatedMatureAtMS != nil {
		t.Fatalf("unexpected mature plot: %+v", plot)
	}
}

func TestSettleGrowingPlotHandlesClockRollbackAndLargeElapsedTime(t *testing.T) {
	now := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	plot := growingPlotAt(now)
	if matured, err := settleGrowingPlot(plot, now.Add(-time.Second).UnixMilli()); err != nil || matured {
		t.Fatalf("clock rollback settlement = matured:%t err:%v", matured, err)
	}
	if plot.SettledGrowthValueScaled9 != 0 || plot.LastSettledAtMS != now.UnixMilli() {
		t.Fatalf("clock rollback changed growth: %+v", plot)
	}

	plot.LastSettledAtMS = 1
	plot.PlantedAtMS = 1
	if matured, err := settleGrowingPlot(plot, math.MaxInt64); err != nil || !matured {
		t.Fatalf("large elapsed settlement = matured:%t err:%v", matured, err)
	}
}

func TestSettleGrowingPlotSplitsFertilizerIntervalExactly(t *testing.T) {
	now := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	plot := growingPlotAt(now)
	plot.FertilizerEffect = &datav1.TimedEffectRecord{
		EffectInstanceId: make([]byte, 16), EffectKind: datav1.EffectKind_FERTILIZER,
		EffectItemOrPestId: 1, ConfigVersion: 1,
		Modifier:  &datav1.RateDecimal6{ScaledValue: 500_000},
		StartAtMs: now.UnixMilli(), EndAtMs: now.Add(60 * time.Second).UnixMilli(),
	}
	matured, err := settleGrowingPlot(plot, now.Add(50*time.Second).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if matured || plot.SettledGrowthValueScaled9 != 75_000_000_000 ||
		plot.EstimatedMatureAtMS == nil ||
		*plot.EstimatedMatureAtMS != now.Add(70*time.Second).UnixMilli() {
		t.Fatalf("unexpected fertilized growth at 50 seconds: %+v", plot)
	}
	matured, err = settleGrowingPlot(plot, now.Add(70*time.Second).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if !matured || plot.State != plotv1.PlotState_MATURE {
		t.Fatalf("fertilized plot did not mature: %+v", plot)
	}
}

func TestActorActivationMaterializesOfflineMaturityAndFlushesIt(t *testing.T) {
	const playerID = uint64(42)
	plantedAt := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	state := NewDevelopmentState(playerID)
	state.CreatedAtMS = plantedAt.UnixMilli()
	state.UpdatedAtMS = plantedAt.UnixMilli()
	state.PlayerSeq = 2
	state.CheckpointRevision = 3
	state.Plots[1] = growingPlotAt(plantedAt)
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.SetNow(func() time.Time { return plantedAt.Add(101 * time.Second) })
	defer runtime.Close()

	response, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		snapshotRequest(playerID, "offline-maturity"))
	if err != nil {
		t.Fatal(err)
	}
	plot := response.GetGetPlayerSnapshotResponse().GetSnapshot().GetPlots()[0]
	if response.GetStateVersion().GetPlayerSeq() != 3 ||
		plot.GetPlotState() != plotv1.PlotState_MATURE ||
		plot.GetHarvestableQuantity() != 3 ||
		plot.GetEstimatedMatureAtMs() != 0 {
		t.Fatalf("offline maturity response: %+v", response)
	}
	if err := runtime.flushDirty(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 1 || store.expectedRevision[0] != 3 ||
		store.saved[0].CheckpointRevision != 4 ||
		store.saved[0].Plots[0].State.String() != "MATURE" {
		t.Fatalf("offline maturity checkpoint: saved=%+v expected=%v", store.saved, store.expectedRevision)
	}
}

func TestOnlineSchedulerMaterializesDuePlot(t *testing.T) {
	const playerID = uint64(42)
	plantedAt := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	state := NewDevelopmentState(playerID)
	state.CreatedAtMS = plantedAt.UnixMilli()
	state.UpdatedAtMS = plantedAt.UnixMilli()
	state.PlayerSeq = 2
	state.CheckpointRevision = 3
	state.Plots[1] = growingPlotAt(plantedAt)
	runtime, err := NewRuntimeWithStore(checkpointLoaderFunc(func(context.Context, uint64) (*State, error) {
		return state, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	runtime.SetNow(func() time.Time { return plantedAt })
	var pushes []*wsv1.WsEnvelope
	if err := runtime.SetPushForwarder(pushForwarderFunc(func(_ context.Context, envelope *wsv1.WsEnvelope) error {
		pushes = append(pushes, envelope)
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	if _, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		snapshotRequest(playerID, "activate-growing")); err != nil {
		t.Fatal(err)
	}
	runtime.SetNow(func() time.Time { return plantedAt.Add(101 * time.Second) })
	runtime.fireDueDeadlines(context.Background())
	response, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		snapshotRequest(playerID, "after-online-maturity"))
	if err != nil {
		t.Fatal(err)
	}
	if response.GetStateVersion().GetPlayerSeq() != 3 ||
		response.GetGetPlayerSnapshotResponse().GetSnapshot().GetPlots()[0].GetPlotState() != plotv1.PlotState_MATURE {
		t.Fatalf("online maturity response: %+v", response)
	}
	if len(pushes) != 1 ||
		pushes[0].GetMessageKind() != wsv1.MessageKind_PUSH ||
		pushes[0].GetRequestId() != "" ||
		pushes[0].GetStateVersion().GetPlayerSeq() != 3 ||
		pushes[0].GetPlayerStateChangedPush().GetReason() != reasonv1.StateChangeReason_MATURED ||
		pushes[0].GetPlayerStateChangedPush().GetPatch().GetPlotUpserts()[0].GetPlotState() != plotv1.PlotState_MATURE {
		t.Fatalf("maturity pushes: %+v", pushes)
	}
}
