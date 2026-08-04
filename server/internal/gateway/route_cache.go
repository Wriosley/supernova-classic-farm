package gateway

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

// RouteSnapshot is one immutable, fully validated committed routing table.
type RouteSnapshot struct {
	MapVersion uint64
	Routes     []Route
}

// RouteSnapshotResolver loads a complete committed routing table.
type RouteSnapshotResolver interface {
	LoadSnapshot(context.Context) (RouteSnapshot, error)
}

// RouteInvalidator conditionally removes the route that produced NOT_OWNER.
type RouteInvalidator interface {
	InvalidateIfVersion(shardID uint32, routeVersion uint64)
}

type routeSlot struct {
	route   Route
	present bool
}

type routeTable struct {
	mapVersion uint64
	slots      [routing.ShardCount]routeSlot
}

// CachedRouteResolver keeps Coordinator lookups off the ordinary command path.
// Writes copy the small fixed table and publish it atomically; per-shard locks
// collapse concurrent misses into one Coordinator request.
type CachedRouteResolver struct {
	source     RouteResolver
	now        func() time.Time
	table      atomic.Pointer[routeTable]
	writeMu    sync.Mutex
	shardLocks [routing.ShardCount]sync.Mutex
}

func NewCachedRouteResolver(
	source RouteResolver,
	now func() time.Time,
) (*CachedRouteResolver, error) {
	if source == nil {
		return nil, errors.New("route source is required")
	}
	if now == nil {
		now = time.Now
	}
	cache := &CachedRouteResolver{source: source, now: now}
	cache.table.Store(&routeTable{})
	return cache, nil
}

// Warm loads a full Coordinator snapshot before Gate starts serving.
func (c *CachedRouteResolver) Warm(ctx context.Context) error {
	loader, ok := c.source.(RouteSnapshotResolver)
	if !ok {
		return errors.New("route source does not support snapshots")
	}
	snapshot, err := loader.LoadSnapshot(ctx)
	if err != nil {
		return err
	}
	if snapshot.MapVersion == 0 || len(snapshot.Routes) != int(routing.ShardCount) {
		return errors.New("route snapshot is incomplete")
	}
	next := &routeTable{mapVersion: snapshot.MapVersion}
	now := c.now().UTC()
	for index, route := range snapshot.Routes {
		if route.ShardID != uint32(index) || !route.usable(now) {
			return errors.New("route snapshot contains an invalid route")
		}
		next.slots[index] = routeSlot{route: route, present: true}
	}
	c.writeMu.Lock()
	c.table.Store(next)
	c.writeMu.Unlock()
	return nil
}

func (c *CachedRouteResolver) Resolve(
	ctx context.Context,
	shardID uint32,
) (Route, error) {
	if shardID >= routing.ShardCount {
		return Route{}, errors.New("shard ID is outside the routing table")
	}
	if route, ok := c.get(shardID); ok {
		return route, nil
	}

	lock := &c.shardLocks[shardID]
	lock.Lock()
	defer lock.Unlock()
	if route, ok := c.get(shardID); ok {
		return route, nil
	}
	route, err := c.source.Resolve(ctx, shardID)
	if err != nil {
		return Route{}, err
	}
	if route.ShardID != shardID || !route.usable(c.now().UTC()) {
		return Route{}, errors.New("resolved route is not usable")
	}
	c.store(route)
	return route, nil
}

func (c *CachedRouteResolver) InvalidateIfVersion(
	shardID uint32,
	routeVersion uint64,
) {
	if shardID >= routing.ShardCount || routeVersion == 0 {
		return
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	current := c.table.Load()
	slot := current.slots[shardID]
	if !slot.present || slot.route.RouteVersion != routeVersion {
		return
	}
	next := *current
	next.slots[shardID] = routeSlot{}
	c.table.Store(&next)
}

func (c *CachedRouteResolver) get(shardID uint32) (Route, bool) {
	slot := c.table.Load().slots[shardID]
	if !slot.present || !slot.route.usable(c.now().UTC()) {
		return Route{}, false
	}
	return slot.route, true
}

func (c *CachedRouteResolver) store(route Route) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	current := c.table.Load()
	slot := current.slots[route.ShardID]
	if slot.present && slot.route.RouteVersion > route.RouteVersion {
		return
	}
	next := *current
	next.slots[route.ShardID] = routeSlot{route: route, present: true}
	if route.MapVersion > next.mapVersion {
		next.mapVersion = route.MapVersion
	}
	c.table.Store(&next)
}

func (r Route) usable(now time.Time) bool {
	return r.ShardID < routing.ShardCount &&
		r.OwnerZoneID != "" &&
		r.OwnerEndpoint != "" &&
		r.OwnerEpoch > 0 &&
		r.RouteVersion > 0 &&
		r.MapVersion > 0 &&
		now.Before(r.LeaseExpiresAt)
}
