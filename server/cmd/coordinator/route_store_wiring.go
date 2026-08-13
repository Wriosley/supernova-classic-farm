package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/routestore"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

const (
	routeStoreLegacyFence = "legacy-fence"
	routeStoreTcaplus     = "tcaplus"
)

func validateRouteStoreMode(mode, storageMode string) (string, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = routeStoreLegacyFence
	}
	switch mode {
	case routeStoreLegacyFence:
		return mode, nil
	case routeStoreTcaplus:
		if strings.TrimSpace(storageMode) != "tcaplus" {
			return "", errors.New("COORDINATOR_ROUTE_STORE=tcaplus requires STORAGE_MODE=tcaplus")
		}
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported COORDINATOR_ROUTE_STORE %q", mode)
	}
}

func bootstrapDurableCurrent(ctx context.Context, store routestore.Store, candidate routestore.Snapshot, now time.Time, leaseDuration time.Duration) (*routing.Map, *routing.RuntimeLeaseOverlay, bool, error) {
	loaded, created, err := store.BootstrapIfEmpty(ctx, candidate)
	if err != nil {
		return nil, nil, false, err
	}
	routes, err := routing.NewMapFromSnapshot(routestore.RoutingSnapshot(loaded))
	if err != nil {
		return nil, nil, false, fmt.Errorf("restore durable Current: %w", err)
	}
	overlay, err := routing.NewRuntimeLeaseOverlay(routes.Snapshot(), now, leaseDuration)
	if err != nil {
		return nil, nil, false, fmt.Errorf("build runtime lease overlay: %w", err)
	}
	return routes, overlay, created, nil
}

func durableBootstrapCandidate(routes *routing.Map, fences []routing.ShardFence, zones []routing.ZoneCandidate, now time.Time, leaseDuration time.Duration) (routestore.Snapshot, error) {
	if err := routing.HydrateActiveRoutesFromFences(routes, fences, zones, now, leaseDuration); err != nil {
		return routestore.Snapshot{}, fmt.Errorf("hydrate durable bootstrap candidate from fences: %w", err)
	}
	return routestore.FromRoutingSnapshot(routes.Snapshot(), now), nil
}

func validateCurrentFences(snapshot routestore.Snapshot, fences []routing.ShardFence, progress map[uint32]*migrationProgress) error {
	if len(fences) != int(routing.ShardCount) || len(snapshot.Entries) != int(routing.ShardCount) {
		return errors.New("Current and Fence sets must both contain 4096 rows")
	}
	for index, entry := range snapshot.Entries {
		fence := fences[index]
		if fence.ShardID != entry.ShardID {
			return fmt.Errorf("Fence order mismatch at shard %d", index)
		}
		switch entry.State {
		case routing.RouteStateActive:
			if fence.OwnerZoneID != entry.OwnerZoneID || fence.OwnerEpoch != entry.OwnerEpoch {
				return fmt.Errorf("ACTIVE Current/Fence mismatch at shard %d", index)
			}
		case routing.RouteStatePreparing:
			migration := progress[entry.ShardID]
			if migration == nil || migration.Prepared.TransitionID != entry.TransitionID {
				return fmt.Errorf("PREPARING Current lacks matching Progress at shard %d", index)
			}
			beforeFence := migration.Step == routing.MigrationStepPreparingCommitted ||
				migration.Step == routing.MigrationStepDrained
			if beforeFence {
				if fence.OwnerZoneID != migration.Source.OwnerZoneID ||
					fence.OwnerEpoch != migration.Source.OwnerEpoch ||
					fence.RouteVersion != migration.Source.RouteVersion {
					return fmt.Errorf("source Fence mismatch at shard %d", index)
				}
			} else if fence.OwnerZoneID != entry.OwnerZoneID ||
				fence.OwnerEpoch != entry.OwnerEpoch || fence.RouteVersion != entry.RouteVersion {
				return fmt.Errorf("target Fence mismatch at shard %d", index)
			}
		case routing.RouteStateUnassigned:
		default:
			return fmt.Errorf("unknown Current state at shard %d", index)
		}
	}
	return nil
}
