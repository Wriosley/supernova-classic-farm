package routing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ShardFence is one durable database ownership fence row.
type ShardFence struct {
	ShardID      uint32
	OwnerZoneID  string
	OwnerEpoch   uint64
	RouteVersion uint64
	TransitionID string
}

// LoadMySQLFences returns all 4096 fence rows in shard-id order.
func LoadMySQLFences(ctx context.Context, db *sql.DB) ([]ShardFence, error) {
	if db == nil {
		return nil, errors.New("MySQL database is required")
	}
	rows, err := db.QueryContext(ctx, `
		SELECT logical_shard_id, owner_zone_id, owner_epoch, route_version,
		       transition_id
		FROM shard_fences
		ORDER BY logical_shard_id`)
	if err != nil {
		return nil, fmt.Errorf("query shard fences: %w", err)
	}
	defer rows.Close()

	fences := make([]ShardFence, 0, ShardCount)
	for rows.Next() {
		var fence ShardFence
		var transition []byte
		if err := rows.Scan(
			&fence.ShardID, &fence.OwnerZoneID, &fence.OwnerEpoch,
			&fence.RouteVersion, &transition,
		); err != nil {
			return nil, fmt.Errorf("scan shard fence: %w", err)
		}
		fence.TransitionID = formatUUIDBytes(transition)
		fences = append(fences, fence)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate shard fences: %w", err)
	}
	if len(fences) != int(ShardCount) {
		return nil, fmt.Errorf(
			"expected %d shard fences, got %d", ShardCount, len(fences),
		)
	}
	for index, fence := range fences {
		if fence.ShardID != uint32(index) {
			return nil, fmt.Errorf(
				"shard fence order mismatch at %d: got %d", index, fence.ShardID,
			)
		}
	}
	return fences, nil
}

// FencesAreEpochOneBootstrap reports whether every fence is still an epoch-one
// / version-one row that ReconcileStaticMySQLFences may accept.
func FencesAreEpochOneBootstrap(
	fences []ShardFence,
	snapshot Snapshot,
) bool {
	if len(fences) != int(ShardCount) ||
		len(snapshot.Entries) != int(ShardCount) {
		return false
	}
	for index, fence := range fences {
		target := snapshot.Entries[index]
		if fence.ShardID != uint32(index) ||
			fence.OwnerEpoch != 1 ||
			fence.RouteVersion != 1 ||
			(fence.OwnerZoneID != DefaultZoneID &&
				fence.OwnerZoneID != target.OwnerZoneID) {
			return false
		}
	}
	return true
}

// HydrateActiveRoutesFromFences replaces in-memory ACTIVE routes from durable
// fences and Zone endpoints. Open PREPARING overlays must be applied after.
func HydrateActiveRoutesFromFences(
	routes *Map,
	fences []ShardFence,
	zones []ZoneCandidate,
	now time.Time,
	leaseDuration time.Duration,
) error {
	if routes == nil {
		return errors.New("route map is required")
	}
	if len(fences) != int(ShardCount) {
		return fmt.Errorf(
			"expected %d shard fences, got %d", ShardCount, len(fences),
		)
	}
	if leaseDuration <= 0 {
		return errors.New("lease duration must be positive")
	}
	validated, err := validateZoneCandidates(zones)
	if err != nil {
		return err
	}
	byID := make(map[string]ZoneCandidate, len(validated))
	for _, zone := range validated {
		byID[zone.ZoneID] = zone
	}
	now = now.UTC()
	for _, fence := range fences {
		zone, exists := byID[fence.OwnerZoneID]
		if !exists {
			return fmt.Errorf(
				"fence shard %d owner %q is not a configured Zone",
				fence.ShardID, fence.OwnerZoneID,
			)
		}
		if fence.OwnerEpoch == 0 || fence.RouteVersion == 0 {
			return fmt.Errorf(
				"fence shard %d has invalid epoch/version", fence.ShardID,
			)
		}
		leaseID, err := newUUID()
		if err != nil {
			return fmt.Errorf(
				"create lease ID for hydrated shard %d: %w", fence.ShardID, err,
			)
		}
		entry := RouteEntry{
			ShardID:        fence.ShardID,
			OwnerZoneID:    fence.OwnerZoneID,
			OwnerEndpoint:  zone.Endpoint,
			OwnerEpoch:     fence.OwnerEpoch,
			RouteVersion:   fence.RouteVersion,
			State:          RouteStateActive,
			LeaseTerm:      1,
			LeaseID:        leaseID,
			LeaseExpiresAt: now.Add(leaseDuration),
			TransitionID:   fence.TransitionID,
			UpdatedAt:      now,
		}
		if err := routes.RestoreActive(entry); err != nil {
			return err
		}
		routes.NoteConsumedEpoch(fence.ShardID, fence.OwnerEpoch)
	}
	return nil
}
