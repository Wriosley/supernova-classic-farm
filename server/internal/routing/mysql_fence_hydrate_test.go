package routing

import (
	"testing"
	"time"
)

func TestRestorePreparingAndActivePreserveEpochHighWater(t *testing.T) {
	now := time.Date(2026, 8, 3, 16, 0, 0, 0, time.UTC)
	routes, err := NewStaticMap(now, time.Minute, []ZoneCandidate{
		{ZoneID: "zone-a", Endpoint: "http://127.0.0.1:8082"},
		{ZoneID: "zone-b", Endpoint: "http://127.0.0.1:8084"},
	})
	if err != nil {
		t.Fatal(err)
	}
	shardID := uint32(0)
	for candidate := uint32(0); candidate < ShardCount; candidate++ {
		entry, entryErr := routes.Entry(candidate)
		if entryErr != nil {
			t.Fatal(entryErr)
		}
		if entry.OwnerZoneID == "zone-a" {
			shardID = candidate
			break
		}
	}
	prepared := RouteEntry{
		ShardID: shardID, OwnerZoneID: "zone-b",
		OwnerEndpoint: "http://127.0.0.1:8084", OwnerEpoch: 2,
		RouteVersion: 2, State: RouteStatePreparing, LeaseTerm: 1,
		LeaseID: "22222222-2222-4222-8222-222222222222",
		LeaseExpiresAt: now.Add(time.Minute), PreviousOwnerZoneID: "zone-a",
		TransitionID: "00112233-4455-6677-8899-aabbccddeeff", UpdatedAt: now,
	}
	if err := routes.RestorePreparing(prepared); err != nil {
		t.Fatal(err)
	}
	source := RouteEntry{
		ShardID: shardID, OwnerZoneID: "zone-a",
		OwnerEndpoint: "http://127.0.0.1:8082", OwnerEpoch: 1,
		RouteVersion: 3, State: RouteStateActive, LeaseTerm: 1,
		LeaseID: "11111111-1111-4111-8111-111111111111",
		LeaseExpiresAt: now.Add(time.Minute), UpdatedAt: now,
	}
	if err := routes.RestoreActive(source); err != nil {
		t.Fatal(err)
	}
	routes.NoteConsumedEpoch(shardID, 2)
	next, err := routes.Prepare(
		shardID, "zone-b", "http://127.0.0.1:8084", now, time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if next.OwnerEpoch != 3 {
		t.Fatalf("Prepare reused abandoned epoch: %+v", next)
	}
}

func TestHydrateActiveRoutesFromFences(t *testing.T) {
	now := time.Date(2026, 8, 3, 16, 0, 0, 0, time.UTC)
	zones := []ZoneCandidate{
		{ZoneID: "zone-a", Endpoint: "http://127.0.0.1:8082"},
		{ZoneID: "zone-b", Endpoint: "http://127.0.0.1:8084"},
	}
	routes, err := NewStaticMap(now, time.Minute, zones)
	if err != nil {
		t.Fatal(err)
	}
	fences := make([]ShardFence, ShardCount)
	for shardID := uint32(0); shardID < ShardCount; shardID++ {
		entry, entryErr := routes.Entry(shardID)
		if entryErr != nil {
			t.Fatal(entryErr)
		}
		owner := entry.OwnerZoneID
		epoch := uint64(1)
		version := uint64(1)
		if shardID == 17 {
			owner = "zone-b"
			epoch = 2
			version = 8
		}
		fences[shardID] = ShardFence{
			ShardID: shardID, OwnerZoneID: owner, OwnerEpoch: epoch,
			RouteVersion: version,
			TransitionID: "00112233-4455-6677-8899-aabbccddeeff",
		}
	}
	if err := HydrateActiveRoutesFromFences(
		routes, fences, zones, now, time.Minute,
	); err != nil {
		t.Fatal(err)
	}
	entry, err := routes.Route(17, now)
	if err != nil {
		t.Fatal(err)
	}
	if entry.OwnerZoneID != "zone-b" || entry.OwnerEpoch != 2 ||
		entry.RouteVersion != 8 {
		t.Fatalf("hydrated route = %+v", entry)
	}
	if FencesAreEpochOneBootstrap(fences, routes.Snapshot()) {
		t.Fatal("advanced fences should not classify as bootstrap")
	}
}
