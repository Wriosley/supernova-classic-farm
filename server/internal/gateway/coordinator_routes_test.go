package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func TestCoordinatorRouteResolverConvertsAndFallsBackAfterInvalidation(t *testing.T) {
	entry := routing.RouteEntry{ShardID: 42, OwnerZoneID: "zone-a", OwnerEndpoint: "http://127.0.0.1:8082", OwnerEpoch: 2, RouteVersion: 3, State: routing.RouteStateActive, LeaseExpiresAt: time.Now().Add(time.Minute)}
	sdk := &fakeCoordinatorSDK{entry: entry, snapshot: routing.Snapshot{MapVersion: 9}}
	fallback := routeFunc(func(context.Context, uint32) (Route, error) {
		return Route{ShardID: 42, OwnerZoneID: "zone-b", OwnerEndpoint: "http://127.0.0.1:8084", OwnerEpoch: 3, RouteVersion: 4, MapVersion: 10, LeaseExpiresAt: time.Now().Add(time.Minute)}, nil
	})
	resolver, err := NewCoordinatorRouteResolver(sdk, fallback)
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolver.Resolve(context.Background(), 42)
	if err != nil || got.OwnerZoneID != "zone-a" || got.MapVersion != 9 {
		t.Fatalf("route=%+v err=%v", got, err)
	}
	resolver.InvalidateIfVersion(42, 3)
	got, err = resolver.Resolve(context.Background(), 42)
	if err != nil || got.OwnerZoneID != "zone-b" || sdk.resyncs != 1 {
		t.Fatalf("fallback=%+v resyncs=%d err=%v", got, sdk.resyncs, err)
	}
}

type fakeCoordinatorSDK struct {
	entry    routing.RouteEntry
	snapshot routing.Snapshot
	resyncs  int
}

func (f *fakeCoordinatorSDK) ResolveShard(uint32) (routing.RouteEntry, error) { return f.entry, nil }
func (f *fakeCoordinatorSDK) Snapshot() routing.Snapshot                      { return f.snapshot }
func (f *fakeCoordinatorSDK) ForceResync()                                    { f.resyncs++ }
