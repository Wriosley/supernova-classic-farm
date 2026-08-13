package coordinatorclient

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	coordinatorv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/coordinator"
	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

var (
	ErrResyncRequired   = errors.New("coordinator route resync required")
	ErrRouteUnavailable = errors.New("coordinator route unavailable")
	ErrWatchStale       = errors.New("coordinator watch is stale")
)

type routeCache struct {
	snapshot     atomic.Pointer[routing.Snapshot]
	availability atomic.Pointer[availabilitySnapshot]
	freshUntil   atomic.Int64
	now          func() time.Time
	ttl          time.Duration
}
type availabilitySnapshot struct {
	version uint64
	zones   map[string]coordinatorv1.ZoneAvailability
}

func newRouteCache(now func() time.Time, ttl time.Duration) *routeCache {
	if now == nil {
		now = time.Now
	}
	return &routeCache{now: now, ttl: ttl}
}
func (c *routeCache) markFresh() { c.freshUntil.Store(c.now().UTC().Add(c.ttl).UnixMilli()) }
func (c *routeCache) applySnapshot(encoded *datav1.ShardMapSnapshot) error {
	snapshot, err := decodeSnapshot(encoded)
	if err != nil {
		return err
	}
	c.snapshot.Store(&snapshot)
	return nil
}
func (c *routeCache) applyBatch(batch *coordinatorv1.RouteBatch) error {
	if batch == nil || batch.MapVersion <= batch.PreviousMapVersion {
		return ErrResyncRequired
	}
	current := c.snapshot.Load()
	if current == nil {
		return ErrResyncRequired
	}
	if batch.MapVersion <= current.MapVersion {
		return nil
	}
	if batch.PreviousMapVersion != current.MapVersion {
		return ErrResyncRequired
	}
	next := *current
	next.Entries = append([]routing.RouteEntry(nil), current.Entries...)
	seen := make(map[uint32]struct{}, len(batch.Routes))
	for _, encoded := range batch.Routes {
		entry, err := decodeRoute(encoded)
		if err != nil || entry.ShardID >= routing.ShardCount {
			return ErrResyncRequired
		}
		if _, exists := seen[entry.ShardID]; exists {
			return ErrResyncRequired
		}
		seen[entry.ShardID] = struct{}{}
		if entry.RouteVersion <= next.Entries[entry.ShardID].RouteVersion {
			return ErrResyncRequired
		}
		next.Entries[entry.ShardID] = entry
	}
	if len(seen) == 0 {
		return ErrResyncRequired
	}
	next.MapVersion = batch.MapVersion
	c.snapshot.Store(&next)
	return nil
}
func (c *routeCache) resolveShard(id uint32) (routing.RouteEntry, error) {
	if id >= routing.ShardCount {
		return routing.RouteEntry{}, ErrRouteUnavailable
	}
	snapshot := c.snapshot.Load()
	if snapshot == nil {
		return routing.RouteEntry{}, ErrRouteUnavailable
	}
	if c.now().UTC().UnixMilli() >= c.freshUntil.Load() {
		return routing.RouteEntry{}, ErrWatchStale
	}
	entry := snapshot.Entries[id]
	if entry.State != routing.RouteStateActive {
		return routing.RouteEntry{}, ErrRouteUnavailable
	}
	if available := c.availability.Load(); available != nil {
		if state, exists := available.zones[entry.OwnerZoneID]; exists && state != coordinatorv1.ZoneAvailability_ZONE_AVAILABILITY_HEALTHY {
			return routing.RouteEntry{}, ErrRouteUnavailable
		}
	}
	return entry, nil
}

func (c *routeCache) applyAvailability(batch *coordinatorv1.AvailabilityBatch) error {
	if batch == nil || batch.AvailabilityVersion <= batch.PreviousAvailabilityVersion {
		return ErrResyncRequired
	}
	current := c.availability.Load()
	currentVersion := uint64(0)
	zones := make(map[string]coordinatorv1.ZoneAvailability)
	if current != nil {
		currentVersion = current.version
		for id, state := range current.zones {
			zones[id] = state
		}
	}
	if batch.AvailabilityVersion <= currentVersion {
		return nil
	}
	if batch.PreviousAvailabilityVersion != currentVersion {
		return ErrResyncRequired
	}
	for _, entry := range batch.Zones {
		if entry.LogicalZoneId == "" || entry.Availability == coordinatorv1.ZoneAvailability_ZONE_AVAILABILITY_UNSPECIFIED {
			return ErrResyncRequired
		}
		zones[entry.LogicalZoneId] = entry.Availability
	}
	c.availability.Store(&availabilitySnapshot{version: batch.AvailabilityVersion, zones: zones})
	return nil
}
func (c *routeCache) getSnapshot() routing.Snapshot {
	current := c.snapshot.Load()
	if current == nil {
		return routing.Snapshot{}
	}
	copy := *current
	copy.Entries = append([]routing.RouteEntry(nil), current.Entries...)
	return copy
}

func decodeSnapshot(encoded *datav1.ShardMapSnapshot) (routing.Snapshot, error) {
	if encoded == nil || encoded.ShardCount != routing.ShardCount || encoded.HashAlgorithmVersion != routing.HashAlgorithmVersion || encoded.AssignmentAlgorithmVersion != routing.AssignmentAlgorithmVersion || encoded.MapVersion == 0 || len(encoded.Entries) != int(routing.ShardCount) {
		return routing.Snapshot{}, ErrResyncRequired
	}
	snapshot := routing.Snapshot{ShardCount: encoded.ShardCount, HashAlgorithmVersion: encoded.HashAlgorithmVersion, AssignmentAlgorithmVersion: encoded.AssignmentAlgorithmVersion, MapVersion: encoded.MapVersion, CommittedTerm: encoded.CommittedTerm, CommittedIndex: encoded.CommittedIndex, Entries: make([]routing.RouteEntry, len(encoded.Entries))}
	for index, item := range encoded.Entries {
		entry, err := decodeRoute(item)
		if err != nil || entry.ShardID != uint32(index) {
			return routing.Snapshot{}, ErrResyncRequired
		}
		snapshot.Entries[index] = entry
	}
	return snapshot, nil
}
func decodeRoute(encoded *datav1.ShardRouteEntry) (routing.RouteEntry, error) {
	if encoded == nil || encoded.RouteVersion == 0 || encoded.OwnerEpoch == 0 {
		return routing.RouteEntry{}, ErrResyncRequired
	}
	state := routing.RouteState("")
	switch encoded.State {
	case datav1.ShardRouteState_ACTIVE:
		state = routing.RouteStateActive
	case datav1.ShardRouteState_PREPARING:
		state = routing.RouteStatePreparing
	case datav1.ShardRouteState_UNASSIGNED:
		state = routing.RouteStateUnassigned
	default:
		return routing.RouteEntry{}, ErrResyncRequired
	}
	lease, err := formatUUID(encoded.LeaseId)
	if err != nil {
		return routing.RouteEntry{}, err
	}
	transition, err := formatUUID(encoded.TransitionId)
	if err != nil {
		return routing.RouteEntry{}, err
	}
	entry := routing.RouteEntry{ShardID: encoded.ShardId, OwnerZoneID: encoded.GetOwnerZoneId(), OwnerEndpoint: encoded.OwnerEndpoint, OwnerEpoch: encoded.OwnerEpoch, RouteVersion: encoded.RouteVersion, State: state, LeaseTerm: encoded.LeaseTerm, LeaseID: lease, LeaseExpiresAt: time.UnixMilli(encoded.LeaseExpiresAtMs).UTC(), PreviousOwnerZoneID: encoded.GetPreviousOwnerZoneId(), TransitionID: transition, UpdatedAt: time.UnixMilli(encoded.UpdatedAtMs).UTC()}
	if state == routing.RouteStateActive && (entry.OwnerZoneID == "" || entry.OwnerEndpoint == "" || entry.LeaseID == "") {
		return routing.RouteEntry{}, ErrResyncRequired
	}
	return entry, nil
}
func formatUUID(value []byte) (string, error) {
	if len(value) == 0 {
		return "", nil
	}
	if len(value) != 16 {
		return "", fmt.Errorf("invalid UUID")
	}
	raw := hex.EncodeToString(value)
	return raw[:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:], nil
}
