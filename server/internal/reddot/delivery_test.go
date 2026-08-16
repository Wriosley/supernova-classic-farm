package reddot

import (
	"context"
	"testing"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/gateway"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

type fakeRoutes map[uint32]gateway.Route

func (f fakeRoutes) Resolve(_ context.Context, shard uint32) (gateway.Route, error) {
	return f[shard], nil
}

type dispatchCall struct {
	route gateway.Route
	ids   []uint64
}
type fakeZones struct {
	calls []dispatchCall
	err   error
}

func (f *fakeZones) DispatchRedDot(_ context.Context, route gateway.Route, ids []uint64, _ *wsv1.RedDotChangedPush) error {
	f.calls = append(f.calls, dispatchCall{route, append([]uint64(nil), ids...)})
	if len(f.calls) == 1 {
		return f.err
	}
	return nil
}

func TestDeliverySeparatesDifferentShardsOnSameEndpoint(t *testing.T) {
	first, second := uint64(1), uint64(2)
	for routing.ShardForPlayer(second) == routing.ShardForPlayer(first) {
		second++
	}
	firstShard, secondShard := routing.ShardForPlayer(first), routing.ShardForPlayer(second)
	routes := fakeRoutes{
		firstShard:  {ShardID: firstShard, OwnerZoneID: "zone-a", OwnerEndpoint: "http://zone-a:8082", OwnerEpoch: 1, RouteVersion: 1},
		secondShard: {ShardID: secondShard, OwnerZoneID: "zone-a", OwnerEndpoint: "http://zone-a:8082", OwnerEpoch: 1, RouteVersion: 1},
	}
	zones := &fakeZones{}
	delivery, err := NewDelivery(routes, zones, nil)
	if err != nil {
		t.Fatal(err)
	}
	delivery.Deliver(context.Background(), []uint64{second, first, first, 0}, &wsv1.RedDotChangedPush{NotificationId: "n", Category: wsv1.RedDotCategory_RED_DOT_CATEGORY_MAIL, Operation: wsv1.RedDotOperation_RED_DOT_OPERATION_SET})
	if len(zones.calls) != 2 {
		t.Fatalf("calls=%d, want 2 shard groups", len(zones.calls))
	}
	for _, call := range zones.calls {
		if len(call.ids) != 1 || call.route.ShardID != routing.ShardForPlayer(call.ids[0]) {
			t.Fatalf("mixed shard call: %+v", call)
		}
	}
}

type invalidatingRoutes struct {
	route       gateway.Route
	invalidated int
}

func (f *invalidatingRoutes) Resolve(_ context.Context, _ uint32) (gateway.Route, error) {
	return f.route, nil
}
func (f *invalidatingRoutes) InvalidateIfVersion(_ uint32, _ uint64) { f.invalidated++ }

func TestDeliveryRetriesNotOwnerOnce(t *testing.T) {
	playerID := uint64(99)
	shardID := routing.ShardForPlayer(playerID)
	routes := &invalidatingRoutes{route: gateway.Route{ShardID: shardID, OwnerZoneID: "zone-a", OwnerEndpoint: "http://zone-a:8082", OwnerEpoch: 1, RouteVersion: 7}}
	zones := &fakeZones{err: gateway.ErrNotOwner}
	delivery, err := NewDelivery(routes, zones, nil)
	if err != nil {
		t.Fatal(err)
	}
	delivery.Deliver(context.Background(), []uint64{playerID}, &wsv1.RedDotChangedPush{NotificationId: "same", Category: wsv1.RedDotCategory_RED_DOT_CATEGORY_MAIL, Operation: wsv1.RedDotOperation_RED_DOT_OPERATION_SET})
	if routes.invalidated != 1 || len(zones.calls) != 2 {
		t.Fatalf("invalidated=%d calls=%d", routes.invalidated, len(zones.calls))
	}
}
