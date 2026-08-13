package gateway

import (
	"context"
	"errors"
	"sync"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

type CoordinatorRouteSDK interface {
	ResolveShard(uint32) (routing.RouteEntry, error)
	Snapshot() routing.Snapshot
	ForceResync()
}
type CoordinatorRouteResolver struct {
	sdk      CoordinatorRouteSDK
	fallback RouteResolver
	mu       sync.Mutex
	invalid  map[uint32]uint64
}

func NewCoordinatorRouteResolver(sdk CoordinatorRouteSDK, fallback RouteResolver) (*CoordinatorRouteResolver, error) {
	if sdk == nil {
		return nil, errors.New("coordinator SDK is required")
	}
	return &CoordinatorRouteResolver{sdk: sdk, fallback: fallback, invalid: make(map[uint32]uint64)}, nil
}
func (r *CoordinatorRouteResolver) Resolve(ctx context.Context, shardID uint32) (Route, error) {
	entry, err := r.sdk.ResolveShard(shardID)
	if err != nil {
		return Route{}, err
	}
	r.mu.Lock()
	invalid := r.invalid[shardID] == entry.RouteVersion
	if !invalid {
		delete(r.invalid, shardID)
	}
	r.mu.Unlock()
	if invalid && r.fallback != nil {
		return r.fallback.Resolve(ctx, shardID)
	}
	snapshot := r.sdk.Snapshot()
	return Route{ShardID: entry.ShardID, OwnerZoneID: entry.OwnerZoneID, OwnerEndpoint: entry.OwnerEndpoint, OwnerEpoch: entry.OwnerEpoch, RouteVersion: entry.RouteVersion, MapVersion: snapshot.MapVersion, LeaseExpiresAt: entry.LeaseExpiresAt}, nil
}
func (r *CoordinatorRouteResolver) InvalidateIfVersion(shardID uint32, routeVersion uint64) {
	if shardID >= routing.ShardCount || routeVersion == 0 {
		return
	}
	r.mu.Lock()
	r.invalid[shardID] = routeVersion
	r.mu.Unlock()
	r.sdk.ForceResync()
}
