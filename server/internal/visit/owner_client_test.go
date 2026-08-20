package visit

import (
	"context"
	"testing"

	"github.com/Wriosley/supernova-classic-farm/server/internal/gateway"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

type recordingRouteResolver struct {
	route              gateway.Route
	resolvedShard      uint32
	invalidatedShard   uint32
	invalidatedVersion uint64
}

func (r *recordingRouteResolver) Resolve(_ context.Context, shardID uint32) (gateway.Route, error) {
	r.resolvedShard = shardID
	return r.route, nil
}

func (r *recordingRouteResolver) InvalidateIfVersion(shardID uint32, version uint64) {
	r.invalidatedShard = shardID
	r.invalidatedVersion = version
}

func TestZoneOwnerFarmClientUsesInjectedRouteSnapshot(t *testing.T) {
	const ownerPlayerID = uint64(9917)
	shardID := routing.ShardForPlayer(ownerPlayerID)
	routes := &recordingRouteResolver{route: gateway.Route{
		ShardID: shardID, OwnerZoneID: "zone-pool-3", OwnerEndpoint: "http://zone-pool-3:8082",
		OwnerEpoch: 7, RouteVersion: 19, MapVersion: 123,
	}}
	client := &ZoneOwnerFarmClient{routes: routes}

	route, err := client.resolve(context.Background(), ownerPlayerID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if routes.resolvedShard != shardID || route.MapVersion != 123 || route.OwnerEpoch != 7 {
		t.Fatalf("resolved route = %+v, shard = %d", route, routes.resolvedShard)
	}
	if !client.invalidate(route) {
		t.Fatal("resolver implements invalidation")
	}
	if routes.invalidatedShard != shardID || routes.invalidatedVersion != 19 {
		t.Fatalf("invalidated shard/version = %d/%d", routes.invalidatedShard, routes.invalidatedVersion)
	}
	committed := committedRoute(route)
	if committed.GetLogicalShardId() != shardID || committed.GetOwnerZoneId() != "zone-pool-3" ||
		committed.GetOwnerEpoch() != 7 || committed.GetRouteVersion() != 19 {
		t.Fatalf("committed route = %+v", committed)
	}
}
