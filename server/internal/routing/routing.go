// Package routing implements the local Coordinator-compatible committed
// ShardMap and the routing/ownership checks shared by backend services.
//
// This package deliberately provides no consensus or high-availability
// guarantees. A Map commit is an in-process, mutex-protected state change.
package routing

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"sync"
	"time"
)

const (
	// ShardCount and HashAlgorithmVersion are fixed by the V1 data contract.
	ShardCount          uint32 = 4096
	HashAlgorithmVersion uint32 = 1

	DefaultZoneID       = "zone-local"
	DefaultZoneEndpoint = "http://127.0.0.1:8082"
)

// RouteState is the committed state of one logical shard route.
type RouteState string

const (
	RouteStateUnassigned RouteState = "UNASSIGNED"
	RouteStatePreparing  RouteState = "PREPARING"
	RouteStateActive     RouteState = "ACTIVE"
)

// RouteEntry is one committed logical shard route.
type RouteEntry struct {
	ShardID             uint32
	OwnerZoneID          string
	OwnerEndpoint        string
	OwnerEpoch           uint64
	RouteVersion         uint64
	State                RouteState
	LeaseTerm            uint64
	LeaseID              string
	LeaseExpiresAt       time.Time
	PreviousOwnerZoneID  string
	TransitionID         string
	UpdatedAt            time.Time
}

// Snapshot is a point-in-time copy of the committed local ShardMap.
type Snapshot struct {
	ShardCount           uint32
	HashAlgorithmVersion uint32
	MapVersion           uint64
	CommittedTerm        uint64
	CommittedIndex       uint64
	Entries              []RouteEntry
}

// Map is a single-node, in-memory committed ShardMap.
type Map struct {
	mu             sync.RWMutex
	mapVersion     uint64
	committedTerm  uint64
	committedIndex uint64
	entries        [ShardCount]RouteEntry
}

// NewLocalMap initializes all 4096 shards as ACTIVE under zone-local at epoch
// one. The caller must renew the leases before they expire.
func NewLocalMap(now time.Time, leaseDuration time.Duration) (*Map, error) {
	if leaseDuration <= 0 {
		return nil, errors.New("lease duration must be positive")
	}
	now = now.UTC()
	m := &Map{
		mapVersion:     1,
		committedTerm:  1,
		committedIndex: 1,
	}
	for shardID := uint32(0); shardID < ShardCount; shardID++ {
		leaseID, err := newUUID()
		if err != nil {
			return nil, fmt.Errorf("create lease ID for shard %d: %w", shardID, err)
		}
		m.entries[shardID] = RouteEntry{
			ShardID:        shardID,
			OwnerZoneID:     DefaultZoneID,
			OwnerEndpoint:   DefaultZoneEndpoint,
			OwnerEpoch:      1,
			RouteVersion:    1,
			State:           RouteStateActive,
			LeaseTerm:       1,
			LeaseID:         leaseID,
			LeaseExpiresAt:  now.Add(leaseDuration),
			UpdatedAt:       now,
		}
	}
	return m, nil
}

// StableHash64 hashes the big-endian uint64 player identity with FNV-1a. This
// is hash algorithm version 1 and must not be changed without a migration.
func StableHash64(playerID uint64) uint64 {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], playerID)
	h := fnv.New64a()
	_, _ = h.Write(encoded[:])
	return h.Sum64()
}

// ShardForPlayer returns stable_hash64(player_id) modulo the fixed shard count.
func ShardForPlayer(playerID uint64) uint32 {
	return uint32(StableHash64(playerID) % uint64(ShardCount))
}

// Entry returns the committed entry even when it is not currently routable.
func (m *Map) Entry(shardID uint32) (RouteEntry, error) {
	if err := validShardID(shardID); err != nil {
		return RouteEntry{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.entries[shardID], nil
}

// Route returns an entry only when it is ACTIVE and its lease is still valid.
func (m *Map) Route(shardID uint32, now time.Time) (RouteEntry, error) {
	entry, err := m.Entry(shardID)
	if err != nil {
		return RouteEntry{}, err
	}
	if entry.State != RouteStateActive {
		return RouteEntry{}, newNotOwner(entry, "", 0, "route is not ACTIVE")
	}
	if !now.UTC().Before(entry.LeaseExpiresAt) {
		return RouteEntry{}, newNotOwner(entry, "", 0, "route lease has expired")
	}
	return entry, nil
}

// ValidateOwner rejects stale, wrong-owner, non-ACTIVE, and expired requests
// with a NOT_OWNER error carrying the latest committed route metadata.
func (m *Map) ValidateOwner(
	shardID uint32,
	ownerZoneID string,
	ownerEpoch uint64,
	now time.Time,
) error {
	entry, err := m.Entry(shardID)
	if err != nil {
		return err
	}
	switch {
	case entry.State != RouteStateActive:
		return newNotOwner(entry, ownerZoneID, ownerEpoch, "route is not ACTIVE")
	case !now.UTC().Before(entry.LeaseExpiresAt):
		return newNotOwner(entry, ownerZoneID, ownerEpoch, "route lease has expired")
	case ownerZoneID != entry.OwnerZoneID:
		return newNotOwner(entry, ownerZoneID, ownerEpoch, "owner Zone does not match")
	case ownerEpoch != entry.OwnerEpoch:
		return newNotOwner(entry, ownerZoneID, ownerEpoch, "owner epoch does not match")
	default:
		return nil
	}
}

// Prepare commits PREPARING for a new ownership grant. It consumes a new,
// never-reused epoch and transition identity.
func (m *Map) Prepare(
	shardID uint32,
	ownerZoneID string,
	ownerEndpoint string,
	now time.Time,
	leaseDuration time.Duration,
) (RouteEntry, error) {
	if err := validShardID(shardID); err != nil {
		return RouteEntry{}, err
	}
	if ownerZoneID == "" || ownerEndpoint == "" {
		return RouteEntry{}, errors.New("owner Zone ID and endpoint are required")
	}
	if leaseDuration <= 0 {
		return RouteEntry{}, errors.New("lease duration must be positive")
	}
	leaseID, err := newUUID()
	if err != nil {
		return RouteEntry{}, fmt.Errorf("create lease ID: %w", err)
	}
	transitionID, err := newUUID()
	if err != nil {
		return RouteEntry{}, fmt.Errorf("create transition ID: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.entries[shardID]
	if current.OwnerEpoch == math.MaxUint64 {
		return RouteEntry{}, errors.New("owner epoch exhausted")
	}
	if current.RouteVersion == math.MaxUint64 {
		return RouteEntry{}, errors.New("route version exhausted")
	}
	now = now.UTC()
	next := RouteEntry{
		ShardID:             shardID,
		OwnerZoneID:          ownerZoneID,
		OwnerEndpoint:        ownerEndpoint,
		OwnerEpoch:           current.OwnerEpoch + 1,
		RouteVersion:         current.RouteVersion + 1,
		State:                RouteStatePreparing,
		LeaseTerm:            m.committedTerm,
		LeaseID:              leaseID,
		LeaseExpiresAt:       now.Add(leaseDuration),
		PreviousOwnerZoneID:  current.OwnerZoneID,
		TransitionID:         transitionID,
		UpdatedAt:            now,
	}
	m.commitEntry(next)
	return next, nil
}

// Activate commits ACTIVE for the exact prepared transition without changing
// its already-consumed owner epoch.
func (m *Map) Activate(
	shardID uint32,
	transitionID string,
	now time.Time,
	leaseDuration time.Duration,
) (RouteEntry, error) {
	if err := validShardID(shardID); err != nil {
		return RouteEntry{}, err
	}
	if transitionID == "" {
		return RouteEntry{}, errors.New("transition ID is required")
	}
	if leaseDuration <= 0 {
		return RouteEntry{}, errors.New("lease duration must be positive")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.entries[shardID]
	if current.State != RouteStatePreparing || current.TransitionID != transitionID {
		return RouteEntry{}, errors.New("prepared transition does not match")
	}
	if current.RouteVersion == math.MaxUint64 {
		return RouteEntry{}, errors.New("route version exhausted")
	}
	current.State = RouteStateActive
	current.RouteVersion++
	current.LeaseExpiresAt = now.UTC().Add(leaseDuration)
	current.UpdatedAt = now.UTC()
	m.commitEntry(current)
	return current, nil
}

// Unassign commits an unroutable entry while preserving the highest consumed
// owner epoch. A later Prepare must advance it.
func (m *Map) Unassign(shardID uint32, now time.Time) (RouteEntry, error) {
	if err := validShardID(shardID); err != nil {
		return RouteEntry{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.entries[shardID]
	if current.RouteVersion == math.MaxUint64 {
		return RouteEntry{}, errors.New("route version exhausted")
	}
	next := RouteEntry{
		ShardID:             shardID,
		OwnerEpoch:          current.OwnerEpoch,
		RouteVersion:        current.RouteVersion + 1,
		State:               RouteStateUnassigned,
		PreviousOwnerZoneID: current.OwnerZoneID,
		UpdatedAt:           now.UTC(),
	}
	m.commitEntry(next)
	return next, nil
}

// RenewOwnedLeases commits one lease extension for every entry currently
// owned by ownerZoneID. It does not make PREPARING or UNASSIGNED routes active.
func (m *Map) RenewOwnedLeases(
	ownerZoneID string,
	now time.Time,
	leaseDuration time.Duration,
) (int, error) {
	if ownerZoneID == "" {
		return 0, errors.New("owner Zone ID is required")
	}
	if leaseDuration <= 0 {
		return 0, errors.New("lease duration must be positive")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.entries {
		entry := m.entries[i]
		if entry.OwnerZoneID == ownerZoneID && entry.RouteVersion == math.MaxUint64 {
			return 0, fmt.Errorf("route version exhausted for shard %d", entry.ShardID)
		}
	}
	now = now.UTC()
	renewed := 0
	for i := range m.entries {
		entry := m.entries[i]
		if entry.OwnerZoneID != ownerZoneID || entry.State == RouteStateUnassigned {
			continue
		}
		entry.RouteVersion++
		entry.LeaseTerm = m.committedTerm
		entry.LeaseExpiresAt = now.Add(leaseDuration)
		entry.UpdatedAt = now
		m.entries[i] = entry
		renewed++
	}
	if renewed > 0 {
		m.mapVersion++
		m.committedIndex++
	}
	return renewed, nil
}

// Snapshot returns an independent copy sorted by shard ID.
func (m *Map) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entries := make([]RouteEntry, ShardCount)
	copy(entries, m.entries[:])
	return Snapshot{
		ShardCount:           ShardCount,
		HashAlgorithmVersion: HashAlgorithmVersion,
		MapVersion:           m.mapVersion,
		CommittedTerm:        m.committedTerm,
		CommittedIndex:       m.committedIndex,
		Entries:              entries,
	}
}

func (m *Map) commitEntry(entry RouteEntry) {
	m.entries[entry.ShardID] = entry
	m.mapVersion++
	m.committedIndex++
}

func validShardID(shardID uint32) error {
	if shardID >= ShardCount {
		return fmt.Errorf("shard ID %d is outside [0,%d)", shardID, ShardCount)
	}
	return nil
}

// NotOwnerError is the stable internal ownership-rejection error.
type NotOwnerError struct {
	Code           string
	Reason         string
	ShardID        uint32
	RequestedZone  string
	RequestedEpoch uint64
	Current        RouteEntry
}

func (e *NotOwnerError) Error() string {
	return fmt.Sprintf(
		"NOT_OWNER: shard %d: %s (requested zone=%q epoch=%d, current zone=%q epoch=%d state=%s)",
		e.ShardID,
		e.Reason,
		e.RequestedZone,
		e.RequestedEpoch,
		e.Current.OwnerZoneID,
		e.Current.OwnerEpoch,
		e.Current.State,
	)
}

// IsNotOwner reports whether err is an ownership rejection.
func IsNotOwner(err error) bool {
	var target *NotOwnerError
	return errors.As(err, &target)
}

func newNotOwner(
	entry RouteEntry,
	requestedZone string,
	requestedEpoch uint64,
	reason string,
) *NotOwnerError {
	return &NotOwnerError{
		Code:           "NOT_OWNER",
		Reason:         reason,
		ShardID:        entry.ShardID,
		RequestedZone:  requestedZone,
		RequestedEpoch: requestedEpoch,
		Current:        entry,
	}
}

func newUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		value[0:4],
		value[4:6],
		value[6:8],
		value[8:10],
		value[10:16],
	), nil
}
