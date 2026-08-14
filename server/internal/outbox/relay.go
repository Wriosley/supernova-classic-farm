package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	eventv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/event"
	mailv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/mail"
	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"google.golang.org/protobuf/proto"
)

const (
	relayStatusPending   uint32 = 1
	relayStatusDelivered uint32 = 3
	defaultRelayInterval        = 2 * time.Second
)

// Store is the durable PlayerOutbox dependency of the Zone relay.
type Store interface {
	GetByID(ctx context.Context, eventID []byte) (*tcaplusv1.PlayerOutbox, error)
	ListPending(ctx context.Context) ([]*tcaplusv1.PlayerOutbox, error)
	MarkDelivered(ctx context.Context, eventID []byte, deliveredAtMS int64) error
}

func (r *Relay) RelayOne(ctx context.Context, eventID []byte) error {
	row, err := r.store.GetByID(ctx, eventID)
	if err != nil {
		return err
	}
	if row == nil || row.RelayStatus == relayStatusDelivered {
		return nil
	}
	if r.owner != nil && !r.owner.OwnsLogicalShard(row.LogicalShardId) {
		return nil
	}
	if row.NextAttemptAtMs > r.now().UnixMilli() {
		return nil
	}
	return r.deliverOne(ctx, row)
}

// GiftMailCreator is MailSvr CreateGiftMail.
type GiftMailCreator interface {
	CreateGiftMail(ctx context.Context, request *mailv1.CreateGiftMailRequest) (*mailv1.CreateGiftMailResponse, error)
}

// ShardOwner reports whether this Zone currently owns a logical shard.
type ShardOwner interface {
	OwnsLogicalShard(logicalShardID uint32) bool
}

// Relay delivers pending CREATE_GIFT_MAIL Outbox rows to MailSvr.
type Relay struct {
	store    Store
	mail     GiftMailCreator
	owner    ShardOwner
	now      func() time.Time
	logger   *slog.Logger
	interval time.Duration
	wake     chan []byte
}

func NewRelay(
	store Store, mail GiftMailCreator, owner ShardOwner, now func() time.Time, logger *slog.Logger,
) (*Relay, error) {
	if store == nil || mail == nil {
		return nil, errors.New("outbox store and mail client are required")
	}
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Relay{
		store: store, mail: mail, owner: owner, now: now, logger: logger,
		interval: defaultRelayInterval, wake: make(chan []byte, 1),
	}, nil
}

// Notify hints that one durable Outbox event is ready for immediate relay.
// It never blocks: PlayerOutbox remains authoritative and the periodic scan
// recovers a dropped hint.
func (r *Relay) Notify(eventID []byte) {
	if r == nil || len(eventID) == 0 {
		return
	}
	id := append([]byte(nil), eventID...)
	select {
	case r.wake <- id:
	default:
	}
}

func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case eventID := <-r.wake:
			if err := r.RelayOne(ctx, eventID); err != nil {
				r.logger.Error("immediate outbox relay failed", "error", err)
			}
		case <-ticker.C:
			if err := r.RelayDue(ctx); err != nil {
				r.logger.Error("outbox relay failed", "error", err)
			}
		}
	}
}

func (r *Relay) RelayDue(ctx context.Context) error {
	rows, err := r.store.ListPending(ctx)
	if err != nil {
		return err
	}
	nowMS := r.now().UnixMilli()
	var firstErr error
	for _, row := range rows {
		if row == nil || row.RelayStatus == relayStatusDelivered {
			continue
		}
		if r.owner != nil && !r.owner.OwnsLogicalShard(row.LogicalShardId) {
			continue
		}
		if row.NextAttemptAtMs > nowMS {
			continue
		}
		if err := r.deliverOne(ctx, row); err != nil {
			r.logger.Warn("outbox deliver failed",
				"event_type", row.EventType,
				"aggregate_player_id", row.AggregatePlayerId,
				"error", err,
			)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (r *Relay) deliverOne(ctx context.Context, row *tcaplusv1.PlayerOutbox) error {
	switch datav1.OutboxEventType(row.EventType) {
	case datav1.OutboxEventType_CREATE_GIFT_MAIL:
		return r.deliverGiftMail(ctx, row)
	case datav1.OutboxEventType_CREATE_REWARD_MAIL:
		// Reward-mail consumer is outside 04-3B; leave pending.
		return nil
	default:
		return fmt.Errorf("unsupported outbox event type %d", row.EventType)
	}
}

func (r *Relay) deliverGiftMail(ctx context.Context, row *tcaplusv1.PlayerOutbox) error {
	payload := &eventv1.CreateGiftMailV1{}
	if err := proto.Unmarshal(row.Payload, payload); err != nil {
		return fmt.Errorf("unmarshal CreateGiftMailV1: %w", err)
	}
	response, err := r.mail.CreateGiftMail(ctx, &mailv1.CreateGiftMailRequest{
		SourceEventId:     append([]byte(nil), row.EventId...),
		SenderPlayerId:    payload.GetSenderPlayerId(),
		SenderDisplayName: payload.GetSenderDisplayName(),
		RecipientPlayerId: payload.GetRecipientPlayerId(),
		CropItemId:        payload.GetCropItemId(),
		Quantity:          payload.GetQuantity(),
		CreatedAtMs:       payload.GetCreatedAtMs(),
	})
	if err != nil {
		return err
	}
	if response.GetError() != nil {
		return fmt.Errorf("mail CreateGiftMail: %s", response.GetError().GetCode().String())
	}
	return r.store.MarkDelivered(ctx, row.EventId, r.now().UnixMilli())
}
