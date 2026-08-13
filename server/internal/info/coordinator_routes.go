package info

import (
	"context"
	"errors"
	"sync"

	"github.com/Wriosley/supernova-classic-farm/server/internal/gateway"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

type CoordinatorRouteSDK interface {
	ResolveShard(uint32) (routing.RouteEntry, error)
	Snapshot() routing.Snapshot
	ForceResync()
}
type CoordinatorRoutes struct {
	sdk      CoordinatorRouteSDK
	fallback gateway.RouteResolver
	mu       sync.Mutex
	invalid  map[uint32]uint64
}

func NewCoordinatorRoutes(sdk CoordinatorRouteSDK, fallback gateway.RouteResolver) (*CoordinatorRoutes, error) {
	if sdk == nil {
		return nil, errors.New("coordinator SDK is required")
	}
	return &CoordinatorRoutes{sdk: sdk, fallback: fallback, invalid: make(map[uint32]uint64)}, nil
}
func (r *CoordinatorRoutes) Resolve(ctx context.Context, shardID uint32) (gateway.Route, error) {
	entry, err := r.sdk.ResolveShard(shardID)
	if err != nil {
		return gateway.Route{}, err
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
	return gateway.Route{ShardID: entry.ShardID, OwnerZoneID: entry.OwnerZoneID, OwnerEndpoint: entry.OwnerEndpoint, OwnerEpoch: entry.OwnerEpoch, RouteVersion: entry.RouteVersion, MapVersion: r.sdk.Snapshot().MapVersion, LeaseExpiresAt: entry.LeaseExpiresAt}, nil
}
func (r *CoordinatorRoutes) InvalidateIfVersion(shardID uint32, version uint64) {
	if shardID >= routing.ShardCount || version == 0 {
		return
	}
	r.mu.Lock()
	r.invalid[shardID] = version
	r.mu.Unlock()
	r.sdk.ForceResync()
}
