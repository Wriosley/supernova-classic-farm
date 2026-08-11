package player

import (
	"context"
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	eventv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/event"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"google.golang.org/protobuf/proto"
)

func giftRequest(playerID, recipient uint64, requestID string, cropItemID, quantity uint32) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_SEND_FRIEND_GIFT, RequestId: requestID, TargetPlayerId: playerID,
		Payload: &wsv1.WsEnvelope_SendFriendGiftRequest{
			SendFriendGiftRequest: &wsv1.SendFriendGiftRequest{
				RecipientPlayerId: recipient, CropItemId: cropItemID, Quantity: quantity,
			},
		},
	}
}

func TestSendFriendGiftDeductsAndWritesOutbox(t *testing.T) {
	const playerID = uint64(42)
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: harvestedSellState(playerID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	requestID := "11111111-1111-4111-8111-111111111111"
	response, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, giftRequest(
		playerID, 99, requestID, 1002, 3,
	))
	if err != nil {
		t.Fatal(err)
	}
	if response.GetError() != nil {
		t.Fatalf("unexpected error %+v", response.GetError())
	}
	payload := response.GetSendFriendGiftResponse()
	if payload == nil || payload.Quantity != 3 || len(payload.OutboxEventId) != 16 {
		t.Fatalf("payload = %+v", payload)
	}
	actor := runtime.actors[playerID]
	if actor.state.Inventory[1002] != 0 {
		t.Fatalf("inventory after gift = %v", actor.state.Inventory)
	}
	if len(actor.state.PendingOutbox) != 1 {
		t.Fatalf("pending outbox = %d", len(actor.state.PendingOutbox))
	}
	pending := actor.state.PendingOutbox[0]
	if pending.EventType != datav1.OutboxEventType_CREATE_GIFT_MAIL {
		t.Fatalf("event type = %v", pending.EventType)
	}
	mail := &eventv1.CreateGiftMailV1{}
	if err := proto.Unmarshal(pending.Payload, mail); err != nil {
		t.Fatal(err)
	}
	if mail.SenderPlayerId != playerID || mail.RecipientPlayerId != 99 ||
		mail.CropItemId != 1002 || mail.Quantity != 3 {
		t.Fatalf("mail payload = %+v", mail)
	}

	replay, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, giftRequest(
		playerID, 99, requestID, 1002, 3,
	))
	if err != nil || !replay.GetReplayed() {
		t.Fatalf("replay = %+v err=%v", replay, err)
	}
	if len(actor.state.PendingOutbox) != 1 {
		t.Fatal("replay must not write a second outbox")
	}
}

func TestSendFriendGiftRejectsSelfAndNonCrop(t *testing.T) {
	const playerID = uint64(7)
	now := time.Date(2026, 8, 12, 11, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: harvestedSellState(playerID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	self := giftRequest(playerID, playerID, "22222222-2222-4222-8222-222222222222", 1002, 1)
	response, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, self)
	if err != nil || response.GetError().GetCode() != wsv1.ErrorCode_CANNOT_FRIEND_SELF {
		t.Fatalf("self gift = %+v err=%v", response, err)
	}

	seed := giftRequest(playerID, 8, "33333333-3333-4333-8333-333333333333", 1001, 1)
	response, err = runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, seed)
	if err != nil || response.GetError().GetCode() != wsv1.ErrorCode_ITEM_NOT_SELLABLE {
		t.Fatalf("seed gift = %+v err=%v", response, err)
	}
}

func TestSendFriendGiftRequestIDConflict(t *testing.T) {
	const playerID = uint64(11)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: harvestedSellState(playerID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	requestID := "44444444-4444-4444-8444-444444444444"
	first, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, giftRequest(
		playerID, 12, requestID, 1002, 1,
	))
	if err != nil || first.GetError() != nil {
		t.Fatalf("first = %+v err=%v", first, err)
	}
	conflict, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, giftRequest(
		playerID, 13, requestID, 1002, 1,
	))
	if err != nil || conflict.GetError().GetCode() != wsv1.ErrorCode_REQUEST_ID_CONFLICT {
		t.Fatalf("conflict = %+v err=%v", conflict, err)
	}
}
