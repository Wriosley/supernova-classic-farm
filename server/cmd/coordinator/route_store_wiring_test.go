package main

import (
	"context"
	"testing"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/routestore"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func TestRouteStoreModeSelection(t *testing.T) {
	for _, test := range []struct {
		mode, storage string
		want          string
		ok            bool
	}{
		{mode: "", storage: "tcaplus", want: routeStoreLegacyFence, ok: true},
		{mode: routeStoreLegacyFence, storage: "", want: routeStoreLegacyFence, ok: true},
		{mode: routeStoreTcaplus, storage: "tcaplus", want: routeStoreTcaplus, ok: true},
		{mode: routeStoreTcaplus, storage: "", ok: false},
		{mode: "unknown", storage: "tcaplus", ok: false},
	} {
		got, err := validateRouteStoreMode(test.mode, test.storage)
		if (err == nil) != test.ok || got != test.want {
			t.Fatalf("mode=%q storage=%q got=%q err=%v", test.mode, test.storage, got, err)
		}
	}
}

func TestBootstrapDurableCurrentUsesStoredRoutesOnRestart(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	store := routestore.NewMemoryStore()
	initial := staticCandidate(t, now, []routing.ZoneCandidate{
		{ZoneID: "zone-a", Endpoint: "http://zone-a:8082"},
		{ZoneID: "zone-b", Endpoint: "http://zone-b:8082"},
	})
	routes, overlay, created, err := bootstrapDurableCurrent(context.Background(), store, initial, now, time.Minute)
	if err != nil || !created || overlay == nil {
		t.Fatalf("bootstrap created=%v overlay=%v err=%v", created, overlay, err)
	}
	current := routes.Snapshot()
	prepared := durablePreparing(current.Entries[42])
	stored, err := store.CommitPreparing(context.Background(), prepared, current.MapVersion)
	if err != nil {
		t.Fatal(err)
	}
	active := prepared
	active.State = routing.RouteStateActive
	active.RouteVersion++
	stored, err = store.CommitActive(context.Background(), active, stored.Metadata.MapVersion)
	if err != nil {
		t.Fatal(err)
	}
	reversed := staticCandidate(t, now, []routing.ZoneCandidate{
		{ZoneID: "zone-b", Endpoint: "http://changed-b:8082"},
		{ZoneID: "zone-a", Endpoint: "http://changed-a:8082"},
	})
	restarted, restartedOverlay, created, err := bootstrapDurableCurrent(context.Background(), store, reversed, now.Add(time.Hour), time.Minute)
	if err != nil || created || restartedOverlay == nil {
		t.Fatalf("restart created=%v overlay=%v err=%v", created, restartedOverlay, err)
	}
	got := restarted.Snapshot()
	if got.MapVersion != stored.Metadata.MapVersion || got.Entries[42] != active {
		t.Fatalf("restart recomputed Current: map=%d route=%+v", got.MapVersion, got.Entries[42])
	}
}

func TestValidateCurrentFencesFailsClosedOnMismatch(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	snapshot := staticCandidate(t, now, []routing.ZoneCandidate{{ZoneID: "zone-a", Endpoint: "http://zone-a:8082"}})
	fences := make([]routing.ShardFence, routing.ShardCount)
	for index, entry := range snapshot.Entries {
		fences[index] = routing.ShardFence{ShardID: uint32(index), OwnerZoneID: entry.OwnerZoneID,
			OwnerEpoch: entry.OwnerEpoch, RouteVersion: entry.RouteVersion}
	}
	if err := validateCurrentFences(snapshot, fences, nil); err != nil {
		t.Fatal(err)
	}
	fences[42].OwnerEpoch++
	if err := validateCurrentFences(snapshot, fences, nil); err == nil {
		t.Fatal("Current/Fence mismatch accepted")
	}
}

func staticCandidate(t *testing.T, now time.Time, zones []routing.ZoneCandidate) routestore.Snapshot {
	t.Helper()
	routes, err := routing.NewStaticMap(now, time.Minute, zones)
	if err != nil {
		t.Fatal(err)
	}
	return routestore.FromRoutingSnapshot(routes.Snapshot(), now)
}

func durablePreparing(current routing.RouteEntry) routing.RouteEntry {
	return routing.RouteEntry{ShardID: current.ShardID, OwnerZoneID: "zone-next",
		OwnerEndpoint: "http://zone-next:8082", OwnerEpoch: current.OwnerEpoch + 1,
		RouteVersion: current.RouteVersion + 1, State: routing.RouteStatePreparing,
		LeaseTerm: current.LeaseTerm, LeaseID: "00112233-4455-6677-8899-aabbccddeeff",
		LeaseExpiresAt: current.LeaseExpiresAt, PreviousOwnerZoneID: current.OwnerZoneID,
		TransitionID: "11112222-3333-4444-8555-666677778888", UpdatedAt: current.UpdatedAt.Add(time.Second)}
}
