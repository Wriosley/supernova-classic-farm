package outbox

import (
	"context"
	"errors"
	"fmt"
	"sync"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/tcaplusdb"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
	"google.golang.org/protobuf/proto"
)

type tcaplusClient interface {
	DoGet(proto.Message, *option.PBOpt, ...uint32) error
	DoUpdate(proto.Message, *option.PBOpt, ...uint32) error
	Traverse(proto.Message) ([]proto.Message, error)
}

// TcaplusStore scans and updates PlayerOutbox rows.
type TcaplusStore struct {
	client tcaplusClient
	zoneID uint32
}

func NewTcaplusStore(client tcaplusClient, zoneID uint32) (*TcaplusStore, error) {
	if client == nil || zoneID == 0 {
		return nil, errors.New("Tcaplus outbox client and zone are required")
	}
	return &TcaplusStore{client: client, zoneID: zoneID}, nil
}

func (s *TcaplusStore) ListPending(ctx context.Context) ([]*tcaplusv1.PlayerOutbox, error) {
	_ = ctx
	rows, err := s.client.Traverse(&tcaplusv1.PlayerOutbox{})
	if err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("traverse PlayerOutbox: %w", err)
	}
	out := make([]*tcaplusv1.PlayerOutbox, 0, len(rows))
	for _, row := range rows {
		record, ok := row.(*tcaplusv1.PlayerOutbox)
		if !ok || record == nil || record.RelayStatus == relayStatusDelivered {
			continue
		}
		out = append(out, record)
	}
	return out, nil
}

func (s *TcaplusStore) MarkDelivered(ctx context.Context, eventID []byte, deliveredAtMS int64) error {
	record := &tcaplusv1.PlayerOutbox{EventId: append([]byte(nil), eventID...)}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("get PlayerOutbox: %w", err)
	}
	if record.RelayStatus == relayStatusDelivered {
		return nil
	}
	record.RelayStatus = relayStatusDelivered
	record.DeliveredAtMs = deliveredAtMS
	record.LastErrorCode = ""
	update := &option.PBOpt{
		Ctx: ctx, Version: opt.Version,
		VersionPolicy: option.CheckDataVersionAutoIncrease,
		ResultFlag:    option.TcaplusResultFlagAllNewValue,
	}
	if err := s.client.DoUpdate(record, update, s.zoneID); err != nil {
		return fmt.Errorf("mark PlayerOutbox delivered: %w", err)
	}
	return nil
}

// MemoryStore is the unit-test Store.
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]*tcaplusv1.PlayerOutbox
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: make(map[string]*tcaplusv1.PlayerOutbox)}
}

func (s *MemoryStore) Put(row *tcaplusv1.PlayerOutbox) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := string(row.EventId)
	s.rows[key] = proto.Clone(row).(*tcaplusv1.PlayerOutbox)
}

func (s *MemoryStore) ListPending(_ context.Context) ([]*tcaplusv1.PlayerOutbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*tcaplusv1.PlayerOutbox, 0, len(s.rows))
	for _, row := range s.rows {
		if row.RelayStatus != relayStatusDelivered {
			out = append(out, proto.Clone(row).(*tcaplusv1.PlayerOutbox))
		}
	}
	return out, nil
}

func (s *MemoryStore) MarkDelivered(_ context.Context, eventID []byte, deliveredAtMS int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[string(eventID)]
	if !ok {
		return ErrNotFound
	}
	row.RelayStatus = relayStatusDelivered
	row.DeliveredAtMs = deliveredAtMS
	return nil
}

func (s *MemoryStore) Get(eventID []byte) (*tcaplusv1.PlayerOutbox, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[string(eventID)]
	if !ok {
		return nil, false
	}
	return proto.Clone(row).(*tcaplusv1.PlayerOutbox), true
}

var ErrNotFound = errors.New("outbox record not found")
