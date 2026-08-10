package player

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
)

func TestPublishFarmViewChangesEmptyDoesNothing(t *testing.T) {
	const playerID = uint64(42)
	broadcaster := newRecordingFarmViewBroadcaster()
	runtime := NewRuntime()
	defer runtime.Close()
	if err := runtime.SetFarmViewDispatcher(broadcaster); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		buySeedsRequest(playerID, "00112233-4455-6677-8899-aabbccddee01", 1)); err != nil {
		t.Fatal(err)
	}

	runtime.mu.Lock()
	a := runtime.actors[playerID]
	runtime.mu.Unlock()
	patch := runtime.publishFarmViewChanges(context.Background(), a, playerID, DomainChanges{})
	if patch != nil {
		t.Fatalf("empty DomainChanges produced patch %+v", patch)
	}
	if a.farmViewSeq != 0 {
		t.Fatalf("farm_view_seq = %d, want 0", a.farmViewSeq)
	}
	if calls, _, _ := broadcaster.snapshot(); calls != 0 {
		t.Fatalf("Enqueue called %d times, want 0", calls)
	}
}

func TestPublishFarmViewChangesMultiPlotBumpsSeqOnce(t *testing.T) {
	const playerID = uint64(42)
	broadcaster := newRecordingFarmViewBroadcaster()
	runtime := NewRuntime()
	defer runtime.Close()
	if err := runtime.SetFarmViewDispatcher(broadcaster); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		buySeedsRequest(playerID, "00112233-4455-6677-8899-aabbccddee00", 3)); err != nil {
		t.Fatal(err)
	}
	for i, plotID := range []uint32{3, 1, 2} {
		reqID := fmt.Sprintf("00112233-4455-6677-8899-aabbccddee%02d", i+1)
		response, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
			plantRequest(playerID, reqID, plotID, 1001))
		if err != nil || response.GetError() != nil {
			t.Fatalf("PLANT plot %d: response=%+v err=%v", plotID, response, err)
		}
		broadcaster.waitForCall(t)
	}

	runtime.mu.Lock()
	a := runtime.actors[playerID]
	runtime.mu.Unlock()
	beforeSeq := a.farmViewSeq
	beforeEpoch := append([]byte(nil), a.farmViewEpoch...)

	changes := DomainChanges{}.PlotChanged(3).PlotChanged(1).PlotChanged(1).PlotChanged(2)
	patch := runtime.publishFarmViewChanges(context.Background(), a, playerID, changes)
	if patch == nil {
		t.Fatal("expected patch")
	}
	broadcaster.waitForCall(t)

	if a.farmViewSeq != beforeSeq+1 {
		t.Fatalf("farm_view_seq = %d, want %d", a.farmViewSeq, beforeSeq+1)
	}
	if patch.GetVersion().GetFarmViewSeq() != beforeSeq+1 {
		t.Fatalf("patch seq = %d, want %d", patch.GetVersion().GetFarmViewSeq(), beforeSeq+1)
	}
	if !bytes.Equal(patch.GetVersion().GetFarmViewEpoch(), beforeEpoch) {
		t.Fatalf("patch epoch = %x, want %x", patch.GetVersion().GetFarmViewEpoch(), beforeEpoch)
	}
	ups := patch.GetPlotUpserts()
	if len(ups) != 3 || ups[0].GetPlotId() != 1 || ups[1].GetPlotId() != 2 || ups[2].GetPlotId() != 3 {
		t.Fatalf("plot_upserts = %+v, want sorted 1,2,3", ups)
	}
	for _, view := range ups {
		if view.GetPlotState() != plotv1.PlotState_GROWING {
			t.Fatalf("plot %d state = %v, want GROWING from post-commit Actor State",
				view.GetPlotId(), view.GetPlotState())
		}
	}
}

func TestFailedPlantDoesNotPublishFarmViewEvent(t *testing.T) {
	const playerID = uint64(42)
	broadcaster := newRecordingFarmViewBroadcaster()
	runtime := NewRuntime()
	defer runtime.Close()
	if err := runtime.SetFarmViewDispatcher(broadcaster); err != nil {
		t.Fatal(err)
	}

	response, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		plantRequest(playerID, "00112233-4455-6677-8899-aabbccddee01", 1, 1001))
	if err != nil {
		t.Fatal(err)
	}
	if response.GetError() == nil {
		t.Fatal("expected PLANT without seeds to fail")
	}

	runtime.mu.Lock()
	a := runtime.actors[playerID]
	runtime.mu.Unlock()
	if a == nil || a.farmViewSeq != 0 {
		t.Fatalf("farm_view_seq after failed PLANT = %d, want 0", a.farmViewSeq)
	}
	if calls, _, _ := broadcaster.snapshot(); calls != 0 {
		t.Fatalf("Enqueue called %d times after failed PLANT, want 0", calls)
	}
}

func TestBuildFarmViewPatchUsesCurrentStateAndSortedIDs(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	plots := map[uint32]*Plot{
		2: {ID: 2, State: plotv1.PlotState_EMPTY},
		1: {ID: 1, State: plotv1.PlotState_GROWING, CropID: 2001, PlantedAtMS: now.UnixMilli()},
		4: {ID: 4, State: plotv1.PlotState_MATURE, CropID: 2001},
	}
	epoch := []byte("0123456789abcdef")
	patch := buildFarmViewPatch(99, epoch, 7, []uint32{4, 1, 4, 2}, plots)
	if patch.GetOwnerPlayerId() != 99 || patch.GetVersion().GetFarmViewSeq() != 7 {
		t.Fatalf("patch identity = %+v", patch)
	}
	if !bytes.Equal(patch.GetVersion().GetFarmViewEpoch(), epoch) {
		t.Fatalf("epoch = %x", patch.GetVersion().GetFarmViewEpoch())
	}
	ups := patch.GetPlotUpserts()
	if len(ups) != 3 || ups[0].GetPlotId() != 1 || ups[1].GetPlotId() != 2 || ups[2].GetPlotId() != 4 {
		t.Fatalf("plot_upserts = %+v", ups)
	}
	if ups[0].GetPlotState() != plotv1.PlotState_GROWING ||
		ups[1].GetPlotState() != plotv1.PlotState_EMPTY ||
		ups[2].GetPlotState() != plotv1.PlotState_MATURE {
		t.Fatalf("unexpected plot states: %+v", ups)
	}
}
