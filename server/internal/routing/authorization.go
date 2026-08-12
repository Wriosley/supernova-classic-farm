package routing

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

type authorizationSnapshot struct {
	mapVersion uint64
	entries    [ShardCount]RouteEntry
}

// AuthorizationTable is the Zone-side read-only view of committed ownership.
// Replacement is atomic so commands never observe a partially refreshed map.
type AuthorizationTable struct {
	zoneID        string
	table         atomic.Pointer[authorizationSnapshot]
	drainMu       sync.RWMutex
	draining      [ShardCount]bool
	drainingEpoch [ShardCount]uint64
}

func NewAuthorizationTable(zoneID string) (*AuthorizationTable, error) {
	if zoneID == "" {
		return nil, errors.New("Zone ID is required")
	}
	return &AuthorizationTable{zoneID: zoneID}, nil
}

func (t *AuthorizationTable) ZoneID() string {
	if t == nil {
		return ""
	}
	return t.zoneID
}

func (t *AuthorizationTable) Replace(snapshot Snapshot) error {
	if snapshot.ShardCount != ShardCount ||
		snapshot.HashAlgorithmVersion != HashAlgorithmVersion ||
		snapshot.AssignmentAlgorithmVersion != AssignmentAlgorithmVersion ||
		snapshot.MapVersion == 0 ||
		len(snapshot.Entries) != int(ShardCount) {
		return errors.New("authorization snapshot metadata is incompatible")
	}
	next := &authorizationSnapshot{mapVersion: snapshot.MapVersion}
	for index, entry := range snapshot.Entries {
		if entry.ShardID != uint32(index) {
			return errors.New("authorization snapshot is not ordered by shard")
		}
		next.entries[index] = entry
	}
	current := t.table.Load()
	if current != nil && current.mapVersion > next.mapVersion {
		return errors.New("authorization snapshot would move map_version backwards")
	}
	if current != nil && current.mapVersion == next.mapVersion {
		for index := range next.entries {
			before := current.entries[index]
			after := next.entries[index]
			beforeExpiry := before.LeaseExpiresAt
			afterExpiry := after.LeaseExpiresAt
			before.LeaseExpiresAt = time.Time{}
			after.LeaseExpiresAt = time.Time{}
			if before != after || afterExpiry.Before(beforeExpiry) {
				return errors.New("same-version authorization refresh changed durable route identity")
			}
		}
		table := next
		t.table.Store(table)
		return nil
	}
	t.table.Store(next)
	t.drainMu.Lock()
	for shardID, entry := range next.entries {
		if t.draining[shardID] &&
			entry.OwnerZoneID == t.zoneID &&
			entry.OwnerEpoch > t.drainingEpoch[shardID] &&
			entry.State == RouteStateActive {
			t.draining[shardID] = false
			t.drainingEpoch[shardID] = 0
		}
	}
	t.drainMu.Unlock()
	return nil
}

func (t *AuthorizationTable) Validate(
	targetPlayerID uint64,
	shardID uint32,
	requestedZoneID string,
	requestedEpoch uint64,
	now time.Time,
) error {
	if shardID >= ShardCount || ShardForPlayer(targetPlayerID) != shardID {
		return errors.New("request shard does not match target player")
	}
	current := t.table.Load()
	if current == nil {
		return errors.New("authorization snapshot is not loaded")
	}
	entry := current.entries[shardID]
	t.drainMu.RLock()
	draining := t.draining[shardID]
	t.drainMu.RUnlock()
	switch {
	case draining:
		return newNotOwner(entry, requestedZoneID, requestedEpoch, "shard is draining")
	case entry.State != RouteStateActive:
		return newNotOwner(entry, requestedZoneID, requestedEpoch, "route is not ACTIVE")
	case !now.UTC().Before(entry.LeaseExpiresAt):
		return newNotOwner(entry, requestedZoneID, requestedEpoch, "route lease has expired")
	case entry.OwnerZoneID != t.zoneID:
		return newNotOwner(entry, requestedZoneID, requestedEpoch, "shard belongs to another Zone")
	case requestedZoneID != t.zoneID:
		return newNotOwner(entry, requestedZoneID, requestedEpoch, "requested Zone does not match")
	case requestedEpoch != entry.OwnerEpoch:
		return newNotOwner(entry, requestedZoneID, requestedEpoch, "owner epoch does not match")
	default:
		return nil
	}
}

// BeginDrain blocks new commands for a currently owned ACTIVE shard before
// the Coordinator changes its committed route.
func (t *AuthorizationTable) BeginDrain(
	shardID uint32,
	ownerEpoch uint64,
	now time.Time,
) (RouteEntry, error) {
	if shardID >= ShardCount {
		return RouteEntry{}, errors.New("shard ID is outside the routing table")
	}
	current := t.table.Load()
	if current == nil {
		return RouteEntry{}, errors.New("authorization snapshot is not loaded")
	}
	entry := current.entries[shardID]
	if entry.State != RouteStateActive ||
		entry.OwnerZoneID != t.zoneID ||
		entry.OwnerEpoch != ownerEpoch ||
		!now.UTC().Before(entry.LeaseExpiresAt) {
		return RouteEntry{}, newNotOwner(
			entry, t.zoneID, ownerEpoch, "cannot drain unowned or stale shard",
		)
	}
	t.drainMu.Lock()
	t.draining[shardID] = true
	t.drainingEpoch[shardID] = ownerEpoch
	t.drainMu.Unlock()
	return entry, nil
}

// Resume clears a drain marker when Coordinator aborts before changing Owner.
func (t *AuthorizationTable) Resume(shardID uint32) {
	if shardID >= ShardCount {
		return
	}
	t.drainMu.Lock()
	t.draining[shardID] = false
	t.drainingEpoch[shardID] = 0
	t.drainMu.Unlock()
}

func (t *AuthorizationTable) IsDraining(shardID uint32, ownerEpoch uint64) bool {
	if shardID >= ShardCount || ownerEpoch == 0 {
		return false
	}
	t.drainMu.RLock()
	defer t.drainMu.RUnlock()
	return t.draining[shardID] && t.drainingEpoch[shardID] == ownerEpoch
}

func (t *AuthorizationTable) Entry(shardID uint32) (RouteEntry, bool) {
	if shardID >= ShardCount {
		return RouteEntry{}, false
	}
	current := t.table.Load()
	if current == nil {
		return RouteEntry{}, false
	}
	return current.entries[shardID], true
}
