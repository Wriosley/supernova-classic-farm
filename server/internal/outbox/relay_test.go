package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	eventv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/event"
	mailv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/mail"
	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"google.golang.org/protobuf/proto"
)

type fakeGiftMail struct {
	calls int
	fail  error
}

func (f *fakeGiftMail) CreateGiftMail(
	_ context.Context, request *mailv1.CreateGiftMailRequest,
) (*mailv1.CreateGiftMailResponse, error) {
	f.calls++
	if f.fail != nil {
		return nil, f.fail
	}
	return &mailv1.CreateGiftMailResponse{MailId: "m1", AlreadyApplied: f.calls > 1}, nil
}

type alwaysOwner struct{}

func (alwaysOwner) OwnsLogicalShard(uint32) bool { return true }

func TestRelayOneDeliversTargetWithoutScanning(t *testing.T) {
	store := &countingStore{MemoryStore: NewMemoryStore()}
	eventID := []byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
	payload, _ := proto.Marshal(&eventv1.CreateGiftMailV1{
		SenderPlayerId: 1, RecipientPlayerId: 2, CropItemId: 1002, Quantity: 1,
	})
	store.Put(&tcaplusv1.PlayerOutbox{
		EventId: eventID, LogicalShardId: 1,
		EventType: uint32(datav1.OutboxEventType_CREATE_GIFT_MAIL),
		Payload:   payload, RelayStatus: relayStatusPending, NextAttemptAtMs: 1,
	})
	relay, err := NewRelay(store, &fakeGiftMail{}, alwaysOwner{}, func() time.Time {
		return time.UnixMilli(200)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := relay.RelayOne(context.Background(), eventID); err != nil {
		t.Fatal(err)
	}
	if store.getCalls != 1 || store.listCalls != 0 {
		t.Fatalf("get=%d list=%d", store.getCalls, store.listCalls)
	}
}

type countingStore struct {
	*MemoryStore
	getCalls  int
	listCalls int
}

func (s *countingStore) GetByID(ctx context.Context, eventID []byte) (*tcaplusv1.PlayerOutbox, error) {
	s.getCalls++
	return s.MemoryStore.GetByID(ctx, eventID)
}

func (s *countingStore) ListPending(ctx context.Context) ([]*tcaplusv1.PlayerOutbox, error) {
	s.listCalls++
	return s.MemoryStore.ListPending(ctx)
}

func TestRelayDeliversGiftAndMarksDone(t *testing.T) {
	store := NewMemoryStore()
	payload, _ := proto.MarshalOptions{Deterministic: true}.Marshal(&eventv1.CreateGiftMailV1{
		SenderPlayerId: 1, SenderDisplayName: "A", RecipientPlayerId: 2,
		CropItemId: 1002, Quantity: 3, CreatedAtMs: 100,
	})
	eventID := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	store.Put(&tcaplusv1.PlayerOutbox{
		EventId: eventID, AggregatePlayerId: 1, LogicalShardId: 1,
		EventType:            uint32(datav1.OutboxEventType_CREATE_GIFT_MAIL),
		EventContractVersion: 1, Payload: payload, RelayStatus: relayStatusPending,
		NextAttemptAtMs: 1,
	})
	mail := &fakeGiftMail{}
	relay, err := NewRelay(store, mail, alwaysOwner{}, func() time.Time {
		return time.UnixMilli(200)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := relay.RelayDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if mail.calls != 1 {
		t.Fatalf("calls=%d", mail.calls)
	}
	row, ok := store.Get(eventID)
	if !ok || row.RelayStatus != relayStatusDelivered {
		t.Fatalf("row=%+v ok=%v", row, ok)
	}

	// Idempotent redelivery path after mark: nothing pending.
	if err := relay.RelayDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if mail.calls != 1 {
		t.Fatalf("delivered row should not redeliver, calls=%d", mail.calls)
	}
}

func TestRelayKeepsPendingWhenMailFails(t *testing.T) {
	store := NewMemoryStore()
	payload, _ := proto.Marshal(&eventv1.CreateGiftMailV1{
		SenderPlayerId: 1, SenderDisplayName: "A", RecipientPlayerId: 2,
		CropItemId: 1002, Quantity: 1, CreatedAtMs: 100,
	})
	eventID := []byte{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9}
	store.Put(&tcaplusv1.PlayerOutbox{
		EventId: eventID, AggregatePlayerId: 1, LogicalShardId: 1,
		EventType: uint32(datav1.OutboxEventType_CREATE_GIFT_MAIL),
		Payload:   payload, RelayStatus: relayStatusPending, NextAttemptAtMs: 1,
	})
	mail := &fakeGiftMail{fail: errors.New("mail down")}
	relay, err := NewRelay(store, mail, alwaysOwner{}, time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := relay.RelayDue(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	row, _ := store.Get(eventID)
	if row.RelayStatus == relayStatusDelivered {
		t.Fatal("must stay pending")
	}
}
