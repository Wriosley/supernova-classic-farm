package routestore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func TestMemoryStoreBootstrapCreatesOnceAndPreservesCurrent(t *testing.T) {
	ctx := context.Background()
	initial := testSnapshot(t)
	store := NewMemoryStore()
	loaded, created, err := store.BootstrapIfEmpty(ctx, initial)
	if err != nil || !created || len(loaded.Entries) != int(routing.ShardCount) {
		t.Fatalf("first bootstrap = created %v entries %d err %v", created, len(loaded.Entries), err)
	}
	prepared := nextPreparing(loaded.Entries[42])
	loaded, err = store.CommitPreparing(ctx, prepared, loaded.Metadata.MapVersion)
	if err != nil {
		t.Fatal(err)
	}
	other := testSnapshot(t)
	other.Entries[42].OwnerZoneID = "wrong-bootstrap-owner"
	reloaded, created, err := store.BootstrapIfEmpty(ctx, other)
	if err != nil || created || reloaded.Entries[42].OwnerZoneID != "zone-next" {
		t.Fatalf("repeat bootstrap overwrote current: created=%v route=%+v err=%v", created, reloaded.Entries[42], err)
	}
}

func TestMemoryStoreCommitsDistinctMapVersionsAndRejectsStaleExpected(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	loaded, _, err := store.BootstrapIfEmpty(ctx, testSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	prepared := nextPreparing(loaded.Entries[42])
	preparedSnapshot, err := store.CommitPreparing(ctx, prepared, loaded.Metadata.MapVersion)
	if err != nil || preparedSnapshot.Metadata.MapVersion != 2 {
		t.Fatalf("prepare map version=%d err=%v", preparedSnapshot.Metadata.MapVersion, err)
	}
	if _, err := store.CommitActive(ctx, nextActive(prepared), 1); !errors.Is(err, ErrRouteConflict) {
		t.Fatalf("stale commit error=%v", err)
	}
	activeSnapshot, err := store.CommitActive(ctx, nextActive(prepared), 2)
	if err != nil || activeSnapshot.Metadata.MapVersion != 3 {
		t.Fatalf("active map version=%d err=%v", activeSnapshot.Metadata.MapVersion, err)
	}
}

func TestMemoryStoreRejectsInvalidBootstrapAndReturnsDeepCopies(t *testing.T) {
	ctx := context.Background()
	invalid := testSnapshot(t)
	invalid.Entries = invalid.Entries[:routing.ShardCount-1]
	if _, _, err := NewMemoryStore().BootstrapIfEmpty(ctx, invalid); !errors.Is(err, ErrRouteStoreCorrupt) {
		t.Fatalf("invalid bootstrap error=%v", err)
	}
	store := NewMemoryStore()
	loaded, _, err := store.BootstrapIfEmpty(ctx, testSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	loaded.Entries[0].OwnerZoneID = "mutated"
	reloaded, err := store.Load(ctx)
	if err != nil || reloaded.Entries[0].OwnerZoneID == "mutated" {
		t.Fatalf("Load leaked mutable state: route=%+v err=%v", reloaded.Entries[0], err)
	}
}

func testSnapshot(t *testing.T) Snapshot {
	t.Helper()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	routes, err := routing.NewStaticMap(now, time.Minute, []routing.ZoneCandidate{
		{ZoneID: "zone-a", Endpoint: "http://zone-a:8082"},
		{ZoneID: "zone-b", Endpoint: "http://zone-b:8082"},
	})
	if err != nil {
		t.Fatal(err)
	}
	current := routes.Snapshot()
	return Snapshot{Metadata: Metadata{
		ShardCount: current.ShardCount, HashAlgorithmVersion: current.HashAlgorithmVersion,
		AssignmentAlgorithmVersion: current.AssignmentAlgorithmVersion,
		MapVersion:                 current.MapVersion, UpdatedAt: now,
	}, Entries: current.Entries}
}

func nextPreparing(current routing.RouteEntry) routing.RouteEntry {
	return routing.RouteEntry{
		ShardID: current.ShardID, OwnerZoneID: "zone-next",
		OwnerEndpoint: "http://zone-next:8082", OwnerEpoch: current.OwnerEpoch + 1,
		RouteVersion: current.RouteVersion + 1, State: routing.RouteStatePreparing,
		LeaseTerm: current.LeaseTerm, LeaseID: "00112233-4455-6677-8899-aabbccddeeff",
		LeaseExpiresAt: current.LeaseExpiresAt, PreviousOwnerZoneID: current.OwnerZoneID,
		TransitionID: "11112222-3333-4444-8555-666677778888", UpdatedAt: current.UpdatedAt.Add(time.Second),
	}
}

func nextActive(prepared routing.RouteEntry) routing.RouteEntry {
	active := prepared
	active.State = routing.RouteStateActive
	active.RouteVersion++
	active.UpdatedAt = active.UpdatedAt.Add(time.Second)
	return active
}
