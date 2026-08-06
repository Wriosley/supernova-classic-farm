package interaction

import (
	"context"
	"sync"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"google.golang.org/protobuf/proto"
)

// MemoryStore is an in-process Store for local/dev Zones that run without
// Tcaplus configured. It provides the same CAS-on-version contract as
// TcaplusStore so the Saga and Reconciler behave identically in both modes.
type MemoryStore struct {
	mu      sync.Mutex
	records map[string]*memoryRecord
}

type memoryRecord struct {
	record  *tcaplusv1.FriendInteraction
	version int32
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{records: make(map[string]*memoryRecord)}
}

func (m *MemoryStore) Get(_ context.Context, interactionID []byte) (*tcaplusv1.FriendInteraction, int32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.records[string(interactionID)]
	if !ok {
		return nil, 0, ErrNotFound
	}
	clone, ok := proto.Clone(entry.record).(*tcaplusv1.FriendInteraction)
	if !ok {
		return nil, 0, errCloneFailed
	}
	return clone, entry.version, nil
}

func (m *MemoryStore) Insert(_ context.Context, record *tcaplusv1.FriendInteraction) (int32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := string(record.InteractionId)
	if _, exists := m.records[key]; exists {
		return 0, ErrAlreadyExists
	}
	clone, ok := proto.Clone(record).(*tcaplusv1.FriendInteraction)
	if !ok {
		return 0, errCloneFailed
	}
	m.records[key] = &memoryRecord{record: clone, version: 1}
	return 1, nil
}

func (m *MemoryStore) Update(
	_ context.Context, record *tcaplusv1.FriendInteraction, expectedVersion int32,
) (int32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := string(record.InteractionId)
	entry, ok := m.records[key]
	if !ok {
		return 0, ErrNotFound
	}
	if entry.version != expectedVersion {
		return 0, errVersionConflict
	}
	clone, ok := proto.Clone(record).(*tcaplusv1.FriendInteraction)
	if !ok {
		return 0, errCloneFailed
	}
	entry.record = clone
	entry.version++
	return entry.version, nil
}

// Traverse gives MemoryStore the same table-scan capability
// TcaplusStore's underlying client has, so a Zone running without Tcaplus
// (development/MySQL modes) can still run the Reconciler's periodic
// ReconcileDue ticker instead of only reconciling on the next client retry.
func (m *MemoryStore) Traverse(proto.Message) ([]proto.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]proto.Message, 0, len(m.records))
	for _, entry := range m.records {
		clone, ok := proto.Clone(entry.record).(*tcaplusv1.FriendInteraction)
		if !ok {
			continue
		}
		rows = append(rows, clone)
	}
	return rows, nil
}

var (
	errCloneFailed     = errNamed("interaction record clone failed")
	errVersionConflict = errNamed("interaction record version conflict")
)

type errNamed string

func (e errNamed) Error() string { return string(e) }
