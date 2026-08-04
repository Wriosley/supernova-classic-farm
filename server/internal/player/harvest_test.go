package player

import (
	"context"
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
	"google.golang.org/protobuf/proto"
)

func harvestRequest(playerID uint64, requestID string, plotID uint32) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion,
		MessageKind:     wsv1.MessageKind_REQUEST,
		Action:          wsv1.Action_HARVEST,
		RequestId:       requestID,
		TargetPlayerId:  playerID,
		Payload: &wsv1.WsEnvelope_HarvestRequest{
			HarvestRequest: &wsv1.HarvestRequest{PlotId: plotID},
		},
	}
}

func matureHarvestState(playerID uint64, now time.Time) *State {
	state := NewDevelopmentState(playerID)
	state.PlayerSeq = 4
	state.CheckpointRevision = 5
	state.CreatedAtMS = now.Add(-2 * time.Minute).UnixMilli()
	state.UpdatedAtMS = now.UnixMilli()
	state.Inventory = map[uint32]uint32{}
	state.Tasks[0].Current = 3
	state.Tasks[1].Current = 1
	state.Tasks[2].Current = 1
	state.Plots[1] = &Plot{
		ID: 1, State: plotv1.PlotState_MATURE,
		CropID: 2001, CropItemID: 1002, CropConfigVersion: 1,
		PlantedAtMS:           now.Add(-100 * time.Second).UnixMilli(),
		MaturityValueScaled9:  100_000_000_000,
		BaseGrowthRateScaled6: 1_000_000,
		BaseYield:             3, SettledGrowthValueScaled9: 100_000_000_000,
		LastSettledAtMS: now.UnixMilli(),
	}
	return state
}

func TestHarvestIsIdempotentAndPersistsNeedCleanup(t *testing.T) {
	const playerID = uint64(42)
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: matureHarvestState(playerID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	request := harvestRequest(playerID, "00112233-4455-6677-8899-aabbccddee21", 1)
	first, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, request)
	if err != nil {
		t.Fatal(err)
	}
	harvest := first.GetHarvestResponse()
	if first.GetError() != nil ||
		first.GetStateVersion().GetPlayerSeq() != 5 ||
		harvest.GetCropItemId() != 1002 ||
		harvest.GetHarvestedQuantity() != 3 ||
		harvest.GetPatch().GetInventoryUpserts()[0].GetQuantity() != 3 ||
		harvest.GetPatch().GetPlotUpserts()[0].GetPlotState() != plotv1.PlotState_NEED_CLEANUP ||
		harvest.GetPatch().GetCurrentChapter().GetTasks()[3].GetCurrentValue() != 1 {
		t.Fatalf("unexpected HARVEST response: %+v", first)
	}
	replay, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, request)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.GetReplayed() || replay.GetStateVersion().GetPlayerSeq() != 5 ||
		!proto.Equal(replay.GetHarvestResponse(), harvest) {
		t.Fatalf("unexpected HARVEST replay: %+v", replay)
	}

	if err := runtime.flushDirty(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 1 || store.expectedRevision[0] != 5 {
		t.Fatalf("unexpected checkpoint writes: count=%d expected=%v",
			len(store.saved), store.expectedRevision)
	}
	checkpoint := store.saved[0]
	if checkpoint.PlayerSeq != 5 ||
		checkpoint.CheckpointRevision != 6 ||
		checkpoint.Plots[0].State != datav1.PlotRecordState_NEED_CLEANUP ||
		checkpoint.Inventory[0].GetItemId() != 1002 ||
		checkpoint.Inventory[0].GetQuantity() != 3 ||
		checkpoint.CurrentChapter.Tasks[3].GetCurrentValue() != 1 {
		t.Fatalf("unexpected HARVEST checkpoint: %+v", checkpoint)
	}
}

func TestHarvestInventoryLimitFailuresAreAtomic(t *testing.T) {
	tests := []struct {
		name      string
		prepare   func(*State)
		wantError wsv1.ErrorCode
	}{
		{
			name: "stack limit",
			prepare: func(state *State) {
				state.Inventory[1002] = 298
			},
			wantError: wsv1.ErrorCode_INVENTORY_STACK_LIMIT,
		},
		{
			name: "type limit",
			prepare: func(state *State) {
				for itemID := uint32(1); itemID <= inventoryTypeLimit; itemID++ {
					state.Inventory[itemID+2_000] = 1
				}
			},
			wantError: wsv1.ErrorCode_INVENTORY_TYPE_LIMIT,
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			const playerID = uint64(42)
			now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
			state := matureHarvestState(playerID, now)
			test.prepare(state)
			store := &recordingCheckpointStore{state: state}
			runtime := NewRuntime()
			runtime.store = store
			runtime.now = func() time.Time { return now }
			defer runtime.Close()

			requestID := []string{
				"00112233-4455-6677-8899-aabbccddee31",
				"00112233-4455-6677-8899-aabbccddee32",
			}[index]
			response, err := runtime.Handle(
				context.Background(), playerID, LocalOwnerEpoch,
				harvestRequest(playerID, requestID, 1),
			)
			if err != nil {
				t.Fatal(err)
			}
			actorState := runtime.actors[playerID].state
			if response.GetError().GetCode() != test.wantError ||
				actorState.PlayerSeq != 4 ||
				actorState.Plots[1].State != plotv1.PlotState_MATURE ||
				actorState.Tasks[3].Current != 0 {
				t.Fatalf("failure was not atomic: response=%+v state=%+v",
					response, actorState)
			}
		})
	}
}
