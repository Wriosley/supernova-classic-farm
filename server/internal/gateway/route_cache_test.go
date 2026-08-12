package gateway

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/protobuf/proto"
)

type recordingRouteSource struct {
	snapshot      RouteSnapshot
	resolved      Route
	snapshotCalls atomic.Int32
	calls         atomic.Int32
}

func TestNotOwnerInvalidatesCachedVersionAndRetriesSameRequest(t *testing.T) {
	now := time.Now().UTC()
	source := &recordingRouteSource{
		snapshot: testRouteSnapshot(now, 1),
		resolved: testRoute(routing.ShardForPlayer(42), now, 2),
	}
	cache, err := NewCachedRouteResolver(source, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Warm(context.Background()); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var mu sync.Mutex
	var requestIDs []string
	var routeVersions []uint64
	zone := zoneFunc(func(
		_ context.Context,
		route Route,
		caller uint64,
		body []byte,
	) ([]byte, error) {
		request := decodeEnvelope(t, body)
		mu.Lock()
		requestIDs = append(requestIDs, request.RequestId)
		routeVersions = append(routeVersions, route.RouteVersion)
		mu.Unlock()
		if calls.Add(1) == 1 {
			return nil, ErrNotOwner
		}
		return proto.Marshal(snapshotResponse(request, caller))
	})
	conn, closeServer := authenticatedConnection(t, zone, cache)
	defer closeServer()
	defer conn.CloseNow()

	writeEnvelope(t, conn, snapshotRequest("cached-same-id", 42))
	if response := readEnvelope(t, conn); response.RequestId != "cached-same-id" {
		t.Fatalf("response request_id = %q", response.RequestId)
	}
	if source.calls.Load() != 1 || calls.Load() != 2 {
		t.Fatalf("source/Zone calls = %d/%d, want 1/2",
			source.calls.Load(), calls.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requestIDs) != 2 ||
		requestIDs[0] != "cached-same-id" ||
		requestIDs[1] != "cached-same-id" ||
		routeVersions[0] != 1 ||
		routeVersions[1] != 2 {
		t.Fatalf("requests=%v route_versions=%v", requestIDs, routeVersions)
	}
}

func (s *recordingRouteSource) LoadSnapshot(context.Context) (RouteSnapshot, error) {
	s.snapshotCalls.Add(1)
	return s.snapshot, nil
}

func (s *recordingRouteSource) Resolve(context.Context, uint32) (Route, error) {
	s.calls.Add(1)
	return s.resolved, nil
}

func TestCachedRouteResolverWarmKeepsCoordinatorOffHitPath(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	routes := testRouteSnapshot(now, 1)
	source := &recordingRouteSource{snapshot: routes}
	cache, err := NewCachedRouteResolver(source, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Warm(context.Background()); err != nil {
		t.Fatal(err)
	}
	if source.snapshotCalls.Load() != 1 {
		t.Fatalf("Coordinator snapshot calls = %d, want 1", source.snapshotCalls.Load())
	}
	for attempt := 0; attempt < 3; attempt++ {
		route, err := cache.Resolve(context.Background(), 42)
		if err != nil || route.ShardID != 42 || route.RouteVersion != 1 {
			t.Fatalf("Resolve() = %+v, %v", route, err)
		}
	}
	if source.calls.Load() != 0 {
		t.Fatalf("Coordinator Resolve calls = %d, want 0", source.calls.Load())
	}
}

func TestCachedRouteResolverConditionalInvalidationAndSingleflight(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	source := &recordingRouteSource{
		snapshot: testRouteSnapshot(now, 1),
		resolved: testRoute(42, now, 2),
	}
	cache, err := NewCachedRouteResolver(source, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Warm(context.Background()); err != nil {
		t.Fatal(err)
	}
	cache.InvalidateIfVersion(42, 99)
	if route, err := cache.Resolve(context.Background(), 42); err != nil ||
		route.RouteVersion != 1 {
		t.Fatalf("newer conditional invalidation removed route: %+v, %v", route, err)
	}
	cache.InvalidateIfVersion(42, 1)

	var wg sync.WaitGroup
	for index := 0; index < 20; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			route, resolveErr := cache.Resolve(context.Background(), 42)
			if resolveErr != nil || route.RouteVersion != 2 {
				t.Errorf("Resolve() = %+v, %v", route, resolveErr)
			}
		}()
	}
	wg.Wait()
	if source.calls.Load() != 1 {
		t.Fatalf("Coordinator Resolve calls = %d, want 1", source.calls.Load())
	}
}

func testRouteSnapshot(now time.Time, routeVersion uint64) RouteSnapshot {
	routes := make([]Route, routing.ShardCount)
	for shardID := uint32(0); shardID < routing.ShardCount; shardID++ {
		routes[shardID] = testRoute(shardID, now, routeVersion)
	}
	return RouteSnapshot{MapVersion: routeVersion, Routes: routes}
}

func testRoute(shardID uint32, now time.Time, routeVersion uint64) Route {
	zoneID := "zone-a"
	endpoint := "http://127.0.0.1:8082"
	if shardID%2 == 1 {
		zoneID = "zone-b"
		endpoint = "http://127.0.0.1:8084"
	}
	return Route{
		ShardID: shardID, OwnerZoneID: zoneID, OwnerEpoch: 1,
		RouteVersion: routeVersion, MapVersion: routeVersion,
		LeaseExpiresAt: now.Add(time.Minute), OwnerEndpoint: endpoint,
	}
}
