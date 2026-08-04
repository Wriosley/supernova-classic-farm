package player

import (
	"context"
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	chapterv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/chapter"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
	"google.golang.org/protobuf/proto"
)

func sellQuantityRequest(
	playerID uint64,
	requestID string,
	itemID, quantity uint32,
	priceVersion uint64,
) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_SELL_CROP, RequestId: requestID, TargetPlayerId: playerID,
		Payload: &wsv1.WsEnvelope_SellCropRequest{
			SellCropRequest: &wsv1.SellCropRequest{
				CropItemId: itemID, ExpectedPriceVersion: priceVersion,
				Amount: &wsv1.SellCropRequest_Quantity{Quantity: quantity},
			},
		},
	}
}

func sellAllRequest(
	playerID uint64,
	requestID string,
	itemID uint32,
	priceVersion uint64,
) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_SELL_CROP, RequestId: requestID, TargetPlayerId: playerID,
		Payload: &wsv1.WsEnvelope_SellCropRequest{
			SellCropRequest: &wsv1.SellCropRequest{
				CropItemId: itemID, ExpectedPriceVersion: priceVersion,
				Amount: &wsv1.SellCropRequest_SellAll{SellAll: true},
			},
		},
	}
}

func harvestedSellState(playerID uint64, now time.Time) *State {
	state := NewDevelopmentState(playerID)
	state.PlayerSeq = 5
	state.CheckpointRevision = 6
	state.CreatedAtMS = now.Add(-3 * time.Minute).UnixMilli()
	state.UpdatedAtMS = now.UnixMilli()
	state.Coins = 4
	state.Inventory = map[uint32]uint32{1001: 2, 1002: 3}
	for index := 0; index < 4; index++ {
		state.Tasks[index].Current = state.Tasks[index].Target
	}
	state.Plots[1] = &Plot{
		ID: 1, State: plotv1.PlotState_NEED_CLEANUP,
		CropID: 2001, CropItemID: 1002, CropConfigVersion: 1,
		PlantedAtMS: now.Add(-2 * time.Minute).UnixMilli(),
		BaseYield:   3,
	}
	return state
}

func TestSellCropQuantityCompletesChapterAndPersists(t *testing.T) {
	const playerID = uint64(42)
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: harvestedSellState(playerID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	request := sellQuantityRequest(
		playerID, "00112233-4455-6677-8899-aabbccddee41",
		developmentCropItemID, 1, developmentCropSellPriceVersion,
	)
	first, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, request)
	if err != nil {
		t.Fatal(err)
	}
	sold := first.GetSellCropResponse()
	if first.GetError() != nil ||
		first.GetStateVersion().GetPlayerSeq() != 6 ||
		sold.GetSoldQuantity() != 1 ||
		sold.GetUnitPrice() != developmentCropSellUnitPrice ||
		sold.GetTotalPrice() != developmentCropSellUnitPrice ||
		sold.GetPatch().GetCoinBalance() != 9 ||
		sold.GetPatch().GetInventoryUpserts()[0].GetQuantity() != 2 ||
		sold.GetPatch().GetCurrentChapter().GetStatus() != chapterv1.ChapterStatus_CLAIMABLE ||
		sold.GetPatch().GetCurrentChapter().GetTasks()[4].GetCurrentValue() != 1 {
		t.Fatalf("unexpected SELL_CROP response: %+v", first)
	}
	replay, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, request)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.GetReplayed() || replay.GetStateVersion().GetPlayerSeq() != 6 ||
		!proto.Equal(replay.GetSellCropResponse(), sold) {
		t.Fatalf("unexpected SELL_CROP replay: %+v", replay)
	}

	if err := runtime.flushDirty(context.Background()); err != nil {
		t.Fatal(err)
	}
	checkpoint := store.saved[0]
	if store.expectedRevision[0] != 6 ||
		checkpoint.PlayerSeq != 6 ||
		checkpoint.CheckpointRevision != 7 ||
		checkpoint.CoinBalance != 9 ||
		checkpoint.Inventory[1].GetItemId() != 1002 ||
		checkpoint.Inventory[1].GetQuantity() != 2 ||
		checkpoint.CurrentChapter.Status != datav1.ChapterRecordStatus_CLAIMABLE {
		t.Fatalf("unexpected SELL_CROP checkpoint: %+v", checkpoint)
	}
	restored, err := StateFromCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Chapter != chapterv1.ChapterStatus_CLAIMABLE {
		t.Fatalf("restored chapter status = %s", restored.Chapter)
	}
}

func TestSellAllResolvesFirstQuantityAndReplaysIt(t *testing.T) {
	const playerID = uint64(42)
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: harvestedSellState(playerID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	request := sellAllRequest(
		playerID, "00112233-4455-6677-8899-aabbccddee42",
		developmentCropItemID, developmentCropSellPriceVersion,
	)
	first, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.GetSellCropResponse().GetSoldQuantity() != 3 ||
		first.GetSellCropResponse().GetPatch().GetCoinBalance() != 19 ||
		len(first.GetSellCropResponse().GetPatch().GetInventoryRemovedItemIds()) != 1 {
		t.Fatalf("unexpected sell-all response: %+v", first)
	}
	replay, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, request)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.GetReplayed() ||
		replay.GetSellCropResponse().GetSoldQuantity() != 3 ||
		replay.GetSellCropResponse().GetPatch().GetCoinBalance() != 19 {
		t.Fatalf("unexpected sell-all replay: %+v", replay)
	}
}

func TestSellCropRejectsPriceInventoryAndUnknownItems(t *testing.T) {
	tests := []struct {
		name      string
		request   *wsv1.WsEnvelope
		wantError wsv1.ErrorCode
	}{
		{
			name: "stale price",
			request: sellQuantityRequest(
				42, "00112233-4455-6677-8899-aabbccddee51",
				developmentCropItemID, 1, developmentCropSellPriceVersion-1,
			),
			wantError: wsv1.ErrorCode_PRICE_CHANGED,
		},
		{
			name: "insufficient quantity",
			request: sellQuantityRequest(
				42, "00112233-4455-6677-8899-aabbccddee52",
				developmentCropItemID, 4, developmentCropSellPriceVersion,
			),
			wantError: wsv1.ErrorCode_INSUFFICIENT_ITEM_QUANTITY,
		},
		{
			name: "not sellable",
			request: sellQuantityRequest(
				42, "00112233-4455-6677-8899-aabbccddee53",
				1001, 1, developmentCropSellPriceVersion,
			),
			wantError: wsv1.ErrorCode_ITEM_NOT_SELLABLE,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
			store := &recordingCheckpointStore{state: harvestedSellState(42, now)}
			runtime := NewRuntime()
			runtime.store = store
			runtime.now = func() time.Time { return now }
			defer runtime.Close()

			response, err := runtime.Handle(
				context.Background(), 42, LocalOwnerEpoch, test.request,
			)
			if err != nil {
				t.Fatal(err)
			}
			state := runtime.actors[42].state
			if response.GetError().GetCode() != test.wantError ||
				state.PlayerSeq != 5 || state.Coins != 4 ||
				state.Inventory[1002] != 3 ||
				state.Chapter != chapterv1.ChapterStatus_IN_PROGRESS {
				t.Fatalf("failure was not atomic: response=%+v state=%+v", response, state)
			}
		})
	}
}
