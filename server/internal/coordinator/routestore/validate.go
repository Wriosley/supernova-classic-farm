package routestore

import (
	"fmt"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func validateSnapshot(snapshot Snapshot) error {
	metadata := snapshot.Metadata
	if metadata.ShardCount != routing.ShardCount ||
		metadata.HashAlgorithmVersion != routing.HashAlgorithmVersion ||
		metadata.AssignmentAlgorithmVersion != routing.AssignmentAlgorithmVersion ||
		metadata.MapVersion == 0 {
		return fmt.Errorf("%w: incompatible metadata %+v", ErrRouteStoreCorrupt, metadata)
	}
	if len(snapshot.Entries) != int(routing.ShardCount) {
		return fmt.Errorf("%w: got %d routes, want %d", ErrRouteStoreCorrupt, len(snapshot.Entries), routing.ShardCount)
	}
	for index, entry := range snapshot.Entries {
		if entry.ShardID != uint32(index) {
			return fmt.Errorf("%w: route %d has shard ID %d", ErrRouteStoreCorrupt, index, entry.ShardID)
		}
		if err := validateStoredEntry(entry); err != nil {
			return fmt.Errorf("%w: route %d: %v", ErrRouteStoreCorrupt, index, err)
		}
	}
	return nil
}

func validateStoredEntry(entry routing.RouteEntry) error {
	if entry.OwnerEpoch == 0 || entry.RouteVersion == 0 || entry.UpdatedAt.IsZero() {
		return fmt.Errorf("epoch, route version and update time must be positive")
	}
	switch entry.State {
	case routing.RouteStateActive:
		if entry.OwnerZoneID == "" || entry.OwnerEndpoint == "" || entry.LeaseID == "" {
			return fmt.Errorf("ACTIVE owner, endpoint and lease are required")
		}
	case routing.RouteStatePreparing:
		if entry.OwnerZoneID == "" || entry.OwnerEndpoint == "" || entry.LeaseID == "" ||
			entry.PreviousOwnerZoneID == "" || entry.TransitionID == "" {
			return fmt.Errorf("PREPARING ownership evidence is required")
		}
	case routing.RouteStateUnassigned:
	default:
		return fmt.Errorf("unknown state %q", entry.State)
	}
	return nil
}

func validatePreparing(current, next routing.RouteEntry) error {
	if current.State != routing.RouteStateActive || next.State != routing.RouteStatePreparing ||
		next.ShardID != current.ShardID || next.OwnerZoneID == current.OwnerZoneID ||
		next.PreviousOwnerZoneID != current.OwnerZoneID || next.OwnerEpoch <= current.OwnerEpoch ||
		next.RouteVersion != current.RouteVersion+1 || next.TransitionID == "" {
		return ErrRouteConflict
	}
	return validateStoredEntry(next)
}

func validateActive(current, next routing.RouteEntry) error {
	if current.State != routing.RouteStatePreparing || next.State != routing.RouteStateActive ||
		next.ShardID != current.ShardID || next.OwnerZoneID != current.OwnerZoneID ||
		next.OwnerEndpoint != current.OwnerEndpoint || next.OwnerEpoch != current.OwnerEpoch ||
		next.RouteVersion != current.RouteVersion+1 || next.TransitionID != current.TransitionID {
		return ErrRouteConflict
	}
	return validateStoredEntry(next)
}

func validateRestoredSource(current, next routing.RouteEntry) error {
	if current.State != routing.RouteStatePreparing || next.State != routing.RouteStateActive ||
		next.ShardID != current.ShardID || next.OwnerZoneID != current.PreviousOwnerZoneID ||
		next.RouteVersion <= current.RouteVersion {
		return ErrRouteConflict
	}
	return validateStoredEntry(next)
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	copy := snapshot
	copy.Entries = cloneEntries(snapshot.Entries)
	return copy
}

func cloneEntries(entries []routing.RouteEntry) []routing.RouteEntry {
	return append([]routing.RouteEntry(nil), entries...)
}
