package player

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	mailv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/mail"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
)

type recordingGiftMailer struct {
	calls    int
	requests []*mailv1.CreateGiftMailRequest
	response *mailv1.CreateGiftMailResponse
	err      error
}

type blockingGiftMailer struct {
	started chan struct{}
	release chan struct{}
	calls   int
}

func (m *recordingGiftMailer) CreateGiftMail(
	_ context.Context, request *mailv1.CreateGiftMailRequest,
) (*mailv1.CreateGiftMailResponse, error) {
	m.calls++
	m.requests = append(m.requests, request)
	if m.err != nil {
		return nil, m.err
	}
	if m.response != nil {
		return m.response, nil
	}
	return &mailv1.CreateGiftMailResponse{MailId: "mail-1"}, nil
}

func (m *blockingGiftMailer) CreateGiftMail(
	_ context.Context, _ *mailv1.CreateGiftMailRequest,
) (*mailv1.CreateGiftMailResponse, error) {
	m.calls++
	close(m.started)
	<-m.release
	return &mailv1.CreateGiftMailResponse{MailId: "mail-blocked"}, nil
}

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

func TestSendFriendGiftDeductsAndCallsMailerSynchronously(t *testing.T) {
	const playerID = uint64(42)
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: harvestedSellState(playerID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.SetNow(func() time.Time { return now })
	mailer := &recordingGiftMailer{}
	runtime.SetGiftMailer(mailer)
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
	if mailer.calls != 1 {
		t.Fatalf("mailer calls = %d", mailer.calls)
	}
	if len(mailer.requests) != 1 {
		t.Fatalf("mailer requests = %d", len(mailer.requests))
	}
	request := mailer.requests[0]
	if request.GetSenderPlayerId() != playerID || request.GetRecipientPlayerId() != 99 ||
		request.GetCropItemId() != 1002 || request.GetQuantity() != 3 {
		t.Fatalf("mailer request = %+v", request)
	}
	if !bytes.Equal(request.GetSourceEventId(), payload.GetOutboxEventId()) {
		t.Fatalf("source event id mismatch: request=%x response=%x",
			request.GetSourceEventId(), payload.GetOutboxEventId())
	}
	actor := runtime.actors[playerID]
	if actor.state.Inventory[1002] != 0 {
		t.Fatalf("inventory after gift = %v", actor.state.Inventory)
	}
	if len(actor.state.PendingOutbox) != 0 {
		t.Fatalf("pending outbox = %d", len(actor.state.PendingOutbox))
	}

	replay, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, giftRequest(
		playerID, 99, requestID, 1002, 3,
	))
	if err != nil || !replay.GetReplayed() {
		t.Fatalf("replay = %+v err=%v", replay, err)
	}
	if mailer.calls != 1 {
		t.Fatalf("replay must not call mailer again, calls=%d", mailer.calls)
	}
}

func TestSendFriendGiftRejectsSelfAndNonCrop(t *testing.T) {
	const playerID = uint64(7)
	now := time.Date(2026, 8, 12, 11, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: harvestedSellState(playerID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.SetNow(func() time.Time { return now })
	runtime.SetGiftMailer(&recordingGiftMailer{})
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
	runtime.SetNow(func() time.Time { return now })
	runtime.SetGiftMailer(&recordingGiftMailer{})
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

func TestSendFriendGiftMailerFailureDoesNotRecordResult(t *testing.T) {
	const playerID = uint64(19)
	now := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: harvestedSellState(playerID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.SetNow(func() time.Time { return now })
	mailer := &recordingGiftMailer{err: errors.New("mail service unavailable")}
	runtime.SetGiftMailer(mailer)
	defer runtime.Close()

	requestID := "55555555-5555-4555-8555-555555555555"
	response, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, giftRequest(
		playerID, 20, requestID, 1002, 1,
	))
	if err != nil || response.GetError().GetCode() != wsv1.ErrorCode_CONFIG_UNAVAILABLE {
		t.Fatalf("response = %+v err=%v", response, err)
	}
	actor := runtime.actors[playerID]
	if actor == nil {
		t.Fatal("actor was not loaded")
	}
	if actor.state.Inventory[1002] != 3 {
		t.Fatalf("inventory after mail failure = %v", actor.state.Inventory)
	}
	if len(actor.state.RecentResults) != 0 {
		t.Fatalf("unexpected recorded result count = %d", len(actor.state.RecentResults))
	}
	if mailer.calls != 1 {
		t.Fatalf("mailer calls = %d", mailer.calls)
	}
}

func TestSendFriendGiftReleasesMailboxWhileWaitingForMailer(t *testing.T) {
	const playerID = uint64(21)
	now := time.Date(2026, 8, 12, 14, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: harvestedSellState(playerID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.SetNow(func() time.Time { return now })
	mailer := &blockingGiftMailer{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	runtime.SetGiftMailer(mailer)
	defer runtime.Close()

	done := make(chan struct{})
	go func() {
		_, _ = runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, giftRequest(
			playerID, 22, "66666666-6666-4666-8666-666666666666", 1002, 1,
		))
		close(done)
	}()
	select {
	case <-mailer.started:
	case <-time.After(time.Second):
		t.Fatal("mailer did not start")
	}
	actor := runtime.actors[playerID]
	if actor == nil {
		t.Fatal("actor was not loaded")
	}
	if !actor.mailbox.Idle() {
		t.Fatal("mailbox should be idle while waiting for mailer")
	}
	close(mailer.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("gift command did not finish")
	}
}
