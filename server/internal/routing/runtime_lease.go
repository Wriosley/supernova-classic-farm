package routing

import (
	"errors"
	"sync/atomic"
	"time"
)

var ErrRuntimeLeaseUnavailable = errors.New("runtime lease unavailable")

type runtimeLeaseBinding struct {
	ownerZoneID  string
	ownerEpoch   uint64
	routeVersion uint64
	leaseID      string
	leaseTerm    uint64
	expiresAt    time.Time
}

type runtimeLeaseSnapshot struct {
	entries [ShardCount]runtimeLeaseBinding
}

// RuntimeLeaseOverlay extends only the process-local expiry of an exact
// durable route identity. It never mutates the durable ShardMap.
type RuntimeLeaseOverlay struct {
	table atomic.Pointer[runtimeLeaseSnapshot]
}

func NewRuntimeLeaseOverlay(snapshot Snapshot, now time.Time, duration time.Duration) (*RuntimeLeaseOverlay, error) {
	overlay := &RuntimeLeaseOverlay{}
	if err := overlay.Renew(snapshot, now, duration); err != nil {
		return nil, err
	}
	return overlay, nil
}

func (o *RuntimeLeaseOverlay) Renew(snapshot Snapshot, now time.Time, duration time.Duration) error {
	if o == nil || duration <= 0 || snapshot.ShardCount != ShardCount ||
		len(snapshot.Entries) != int(ShardCount) {
		return errors.New("valid route snapshot and lease duration are required")
	}
	next := &runtimeLeaseSnapshot{}
	expiresAt := now.UTC().Add(duration)
	for index, entry := range snapshot.Entries {
		if entry.ShardID != uint32(index) {
			return errors.New("runtime lease snapshot is not ordered")
		}
		if entry.State != RouteStateActive {
			continue
		}
		next.entries[index] = runtimeLeaseBinding{
			ownerZoneID: entry.OwnerZoneID, ownerEpoch: entry.OwnerEpoch,
			routeVersion: entry.RouteVersion, leaseID: entry.LeaseID,
			leaseTerm: entry.LeaseTerm, expiresAt: expiresAt,
		}
	}
	o.table.Store(next)
	return nil
}

func (o *RuntimeLeaseOverlay) Effective(entry RouteEntry, now time.Time) (RouteEntry, error) {
	if o == nil || entry.ShardID >= ShardCount || entry.State != RouteStateActive {
		return RouteEntry{}, ErrRuntimeLeaseUnavailable
	}
	current := o.table.Load()
	if current == nil {
		return RouteEntry{}, ErrRuntimeLeaseUnavailable
	}
	binding := current.entries[entry.ShardID]
	if binding.ownerZoneID != entry.OwnerZoneID || binding.ownerEpoch != entry.OwnerEpoch ||
		binding.routeVersion != entry.RouteVersion || binding.leaseID != entry.LeaseID ||
		binding.leaseTerm != entry.LeaseTerm || !now.UTC().Before(binding.expiresAt) {
		return RouteEntry{}, ErrRuntimeLeaseUnavailable
	}
	effective := entry
	effective.LeaseExpiresAt = binding.expiresAt
	return effective, nil
}
