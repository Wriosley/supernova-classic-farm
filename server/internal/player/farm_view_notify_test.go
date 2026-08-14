package player

import (
	"context"
	"sync"
	"testing"
	"time"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
)

type recordingFarmViewBroadcaster struct {
	mu      sync.Mutex
	calls   int
	last    *wsv1.FarmViewPatch
	ownerID uint64
	done    chan struct{}
}

func newRecordingFarmViewBroadcaster() *recordingFarmViewBroadcaster {
	return &recordingFarmViewBroadcaster{done: make(chan struct{}, 16)}
}

func (b *recordingFarmViewBroadcaster) Enqueue(ownerPlayerID uint64, patch *wsv1.FarmViewPatch) {
	b.mu.Lock()
	b.calls++
	b.last = patch
	b.ownerID = ownerPlayerID
	b.mu.Unlock()
	b.done <- struct{}{}
}

func (b *recordingFarmViewBroadcaster) Close() {}

func (b *recordingFarmViewBroadcaster) waitForCall(t *testing.T) {
	t.Helper()
	select {
	case <-b.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for FarmViewDispatcher.Enqueue")
	}
}

func (b *recordingFarmViewBroadcaster) snapshot() (int, *wsv1.FarmViewPatch, uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls, b.last, b.ownerID
}

func TestBuySeedsDoesNotBumpFarmViewSeq(t *testing.T) {
	const playerID = uint64(42)
	broadcaster := newRecordingFarmViewBroadcaster()
	runtime := NewRuntime()
	defer runtime.Close()
	if err := runtime.SetFarmViewDispatcher(broadcaster); err != nil {
		t.Fatal(err)
	}

	if _, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		buySeedsRequest(playerID, "00112233-4455-6677-8899-aabbccddee01", 3)); err != nil {
		t.Fatal(err)
	}

	runtime.mu.Lock()
	actor := runtime.actors[playerID]
	runtime.mu.Unlock()
	if actor == nil {
		t.Fatal("actor not activated")
	}
	if actor.farmViewSeq != 0 {
		t.Fatalf("farm_view_seq after BUY_SEEDS = %d, want 0", actor.farmViewSeq)
	}
	if calls, _, _ := broadcaster.snapshot(); calls != 0 {
		t.Fatalf("Broadcast called %d times after BUY_SEEDS, want 0", calls)
	}
}

func TestPlantBumpsFarmViewSeqAndBroadcastsPatch(t *testing.T) {
	const playerID = uint64(42)
	broadcaster := newRecordingFarmViewBroadcaster()
	runtime := NewRuntime()
	defer runtime.Close()
	if err := runtime.SetFarmViewDispatcher(broadcaster); err != nil {
		t.Fatal(err)
	}

	if _, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		buySeedsRequest(playerID, "00112233-4455-6677-8899-aabbccddee01", 3)); err != nil {
		t.Fatal(err)
	}
	response, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		plantRequest(playerID, "00112233-4455-6677-8899-aabbccddee02", 1, 1001))
	if err != nil {
		t.Fatal(err)
	}
	if response.GetError() != nil {
		t.Fatalf("PLANT failed: %+v", response.GetError())
	}

	runtime.mu.Lock()
	actor := runtime.actors[playerID]
	runtime.mu.Unlock()
	if actor == nil || actor.farmViewSeq != 1 {
		t.Fatalf("farm_view_seq after PLANT = %d, want 1", actor.farmViewSeq)
	}

	broadcaster.waitForCall(t)
	calls, patch, ownerID := broadcaster.snapshot()
	if calls != 1 {
		t.Fatalf("Broadcast called %d times after PLANT, want 1", calls)
	}
	if ownerID != playerID {
		t.Fatalf("Broadcast owner_player_id = %d, want %d", ownerID, playerID)
	}
	if patch.GetVersion().GetFarmViewSeq() != 1 {
		t.Fatalf("patch farm_view_seq = %d, want 1", patch.GetVersion().GetFarmViewSeq())
	}
	if len(patch.GetVersion().GetFarmViewEpoch()) != 16 {
		t.Fatalf("patch farm_view_epoch length = %d, want 16", len(patch.GetVersion().GetFarmViewEpoch()))
	}
	if len(patch.GetPlotUpserts()) != 1 ||
		patch.GetPlotUpserts()[0].GetPlotId() != 1 ||
		patch.GetPlotUpserts()[0].GetPlotState() != plotv1.PlotState_GROWING {
		t.Fatalf("patch plot_upserts = %+v", patch.GetPlotUpserts())
	}
}

func TestHarvestAndCleanPlotEachBumpFarmViewSeqOnce(t *testing.T) {
	const playerID = uint64(42)
	fixedNow := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	broadcaster := newRecordingFarmViewBroadcaster()
	runtime := NewRuntime()
	runtime.SetNow(func() time.Time { return fixedNow })
	defer runtime.Close()
	if err := runtime.SetFarmViewDispatcher(broadcaster); err != nil {
		t.Fatal(err)
	}

	if _, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		buySeedsRequest(playerID, "00112233-4455-6677-8899-aabbccddee01", 3)); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		plantRequest(playerID, "00112233-4455-6677-8899-aabbccddee02", 1, 1001)); err != nil {
		t.Fatal(err)
	}
	broadcaster.waitForCall(t)

	// Fast-forward past maturity so HARVEST succeeds against a MATURE plot.
	runtime.SetNow(func() time.Time { return fixedNow.Add(200 * time.Second) })
	harvestResponse, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		&wsv1.WsEnvelope{
			ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
			Action: wsv1.Action_HARVEST, RequestId: "00112233-4455-6677-8899-aabbccddee03",
			TargetPlayerId: playerID,
			Payload:        &wsv1.WsEnvelope_HarvestRequest{HarvestRequest: &wsv1.HarvestRequest{PlotId: 1}},
		})
	if err != nil || harvestResponse.GetError() != nil {
		t.Fatalf("HARVEST failed: response=%+v error=%v", harvestResponse, err)
	}
	broadcaster.waitForCall(t)

	cleanResponse, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		&wsv1.WsEnvelope{
			ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
			Action: wsv1.Action_CLEAN_PLOT, RequestId: "00112233-4455-6677-8899-aabbccddee04",
			TargetPlayerId: playerID,
			Payload:        &wsv1.WsEnvelope_CleanPlotRequest{CleanPlotRequest: &wsv1.CleanPlotRequest{PlotId: 1}},
		})
	if err != nil || cleanResponse.GetError() != nil {
		t.Fatalf("CLEAN_PLOT failed: response=%+v error=%v", cleanResponse, err)
	}
	broadcaster.waitForCall(t)

	runtime.mu.Lock()
	actor := runtime.actors[playerID]
	runtime.mu.Unlock()
	if actor == nil || actor.farmViewSeq != 3 {
		t.Fatalf("farm_view_seq after PLANT+HARVEST+CLEAN_PLOT = %d, want 3", actor.farmViewSeq)
	}
	if calls, _, _ := broadcaster.snapshot(); calls != 3 {
		t.Fatalf("Broadcast called %d times, want 3", calls)
	}
}

func TestRuntimeWorksWithoutFarmViewDispatcherConfigured(t *testing.T) {
	const playerID = uint64(42)
	runtime := NewRuntime()
	defer runtime.Close()

	response, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		buySeedsRequest(playerID, "00112233-4455-6677-8899-aabbccddee01", 3))
	if err != nil || response.GetError() != nil {
		t.Fatalf("BUY_SEEDS without broadcaster: response=%+v error=%v", response, err)
	}
	plantResponse, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		plantRequest(playerID, "00112233-4455-6677-8899-aabbccddee02", 1, 1001))
	if err != nil || plantResponse.GetError() != nil {
		t.Fatalf("PLANT without broadcaster: response=%+v error=%v", plantResponse, err)
	}

	runtime.mu.Lock()
	actor := runtime.actors[playerID]
	runtime.mu.Unlock()
	if actor == nil || actor.farmViewSeq != 1 {
		t.Fatalf("farm_view_seq without broadcaster configured = %d, want 1", actor.farmViewSeq)
	}
}
