package coordinatorclient

import (
	"errors"
	"testing"
	"time"

	coordinatorv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/coordinator"
	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/publisher"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func TestCacheAppliesContiguousBatchAndRejectsGap(t *testing.T) {
	now := time.Date(2026, 8, 13, 14, 0, 0, 0, time.UTC)
	m, _ := routing.NewLocalMap(now, time.Minute)
	initial := m.Snapshot()
	encoded, _ := publisher.SnapshotProto(initial)
	c := newRouteCache(func() time.Time { return now }, 90*time.Second)
	if err := c.applySnapshot(encoded); err != nil {
		t.Fatal(err)
	}
	c.markFresh()
	next := initial
	next.Entries = append([]routing.RouteEntry(nil), initial.Entries...)
	next.MapVersion = 2
	next.Entries[42].RouteVersion++
	next.Entries[42].UpdatedAt = now.Add(time.Second)
	route, _ := publisher.RouteProto(next.Entries[42])
	if err := c.applyBatch(&coordinatorv1.RouteBatch{PreviousMapVersion: 1, MapVersion: 2, Routes: []*datav1.ShardRouteEntry{route}}); err != nil {
		t.Fatal(err)
	}
	got, err := c.resolveShard(42)
	if err != nil || got.RouteVersion != 2 {
		t.Fatalf("route=%+v err=%v", got, err)
	}
	if err := c.applyBatch(&coordinatorv1.RouteBatch{PreviousMapVersion: 3, MapVersion: 4, Routes: []*datav1.ShardRouteEntry{route}}); !errors.Is(err, ErrResyncRequired) {
		t.Fatalf("gap err=%v", err)
	}
}

func TestCacheFailsClosedForPreparingAndStaleWatch(t *testing.T) {
	now := time.Date(2026, 8, 13, 14, 0, 0, 0, time.UTC)
	current := now
	m, _ := routing.NewLocalMap(now, time.Minute)
	snapshot := m.Snapshot()
	snapshot.Entries[7].State = routing.RouteStatePreparing
	encoded, _ := publisher.SnapshotProto(snapshot)
	c := newRouteCache(func() time.Time { return current }, 90*time.Second)
	if err := c.applySnapshot(encoded); err != nil {
		t.Fatal(err)
	}
	c.markFresh()
	if _, err := c.resolveShard(7); !errors.Is(err, ErrRouteUnavailable) {
		t.Fatalf("preparing err=%v", err)
	}
	current = now.Add(91 * time.Second)
	if _, err := c.resolveShard(8); !errors.Is(err, ErrWatchStale) {
		t.Fatalf("stale err=%v", err)
	}
}

func TestCacheAuthoritativeAvailabilityReplacesEvenAfterVersionReset(t *testing.T) {
	cache := newRouteCache(time.Now, time.Minute)
	if err := cache.applyAvailability(&coordinatorv1.AvailabilityBatch{PreviousAvailabilityVersion: 0, AvailabilityVersion: 9, Zones: []*coordinatorv1.ZoneAvailabilityEntry{
		{LogicalZoneId: "zone-a", Availability: coordinatorv1.ZoneAvailability_ZONE_AVAILABILITY_DEAD},
		{LogicalZoneId: "removed-zone", Availability: coordinatorv1.ZoneAvailability_ZONE_AVAILABILITY_HEALTHY},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := cache.applyAvailability(&coordinatorv1.AvailabilityBatch{PreviousAvailabilityVersion: 0, AvailabilityVersion: 1, Zones: []*coordinatorv1.ZoneAvailabilityEntry{
		{LogicalZoneId: "zone-a", Availability: coordinatorv1.ZoneAvailability_ZONE_AVAILABILITY_HEALTHY},
	}}); err != nil {
		t.Fatal(err)
	}
	got := cache.availability.Load()
	if got.version != 1 || len(got.zones) != 1 || got.zones["zone-a"] != coordinatorv1.ZoneAvailability_ZONE_AVAILABILITY_HEALTHY {
		t.Fatalf("availability=%+v", got)
	}
}
