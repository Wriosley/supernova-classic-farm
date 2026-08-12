package routing

import (
	"errors"
	"testing"
	"time"
)

func TestNewLocalMapInitializesAllShardsActive(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	routes, err := NewLocalMap(now, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	snapshot := routes.Snapshot()
	if snapshot.ShardCount != ShardCount {
		t.Fatalf("shard count = %d, want %d", snapshot.ShardCount, ShardCount)
	}
	if snapshot.HashAlgorithmVersion != 1 {
		t.Fatalf("hash version = %d, want 1", snapshot.HashAlgorithmVersion)
	}
	if snapshot.AssignmentAlgorithmVersion != AssignmentAlgorithmVersion {
		t.Fatalf("assignment version = %d, want %d",
			snapshot.AssignmentAlgorithmVersion, AssignmentAlgorithmVersion)
	}
	if len(snapshot.Entries) != int(ShardCount) {
		t.Fatalf("entries = %d, want %d", len(snapshot.Entries), ShardCount)
	}
	for shardID, entry := range snapshot.Entries {
		if entry.ShardID != uint32(shardID) {
			t.Fatalf("entry %d has shard ID %d", shardID, entry.ShardID)
		}
		if entry.OwnerZoneID != DefaultZoneID ||
			entry.OwnerEndpoint != DefaultZoneEndpoint ||
			entry.OwnerEpoch != 1 ||
			entry.RouteVersion != 1 ||
			entry.State != RouteStateActive {
			t.Fatalf("unexpected initial route %d: %+v", shardID, entry)
		}
		if entry.LeaseID == "" || !entry.LeaseExpiresAt.Equal(now.Add(30*time.Second)) {
			t.Fatalf("invalid initial lease for route %d: %+v", shardID, entry)
		}
	}
}

func TestStaticMapUsesStableRendezvousPlacement(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	zones := []ZoneCandidate{
		{ZoneID: "zone-b", Endpoint: "http://127.0.0.1:8084"},
		{ZoneID: "zone-a", Endpoint: "http://127.0.0.1:8082"},
	}
	routes, err := NewStaticMap(now, time.Minute, zones)
	if err != nil {
		t.Fatal(err)
	}
	reordered, err := NewStaticMap(now, time.Minute, []ZoneCandidate{zones[1], zones[0]})
	if err != nil {
		t.Fatal(err)
	}

	counts := map[string]int{}
	snapshot := routes.Snapshot()
	reorderedSnapshot := reordered.Snapshot()
	for shardID := uint32(0); shardID < ShardCount; shardID++ {
		entry := snapshot.Entries[shardID]
		other := reorderedSnapshot.Entries[shardID]
		if entry.OwnerZoneID != other.OwnerZoneID ||
			entry.OwnerEndpoint != other.OwnerEndpoint {
			t.Fatalf("candidate order changed shard %d placement", shardID)
		}
		counts[entry.OwnerZoneID]++
	}
	if counts["zone-a"] == 0 || counts["zone-b"] == 0 {
		t.Fatalf("Rendezvous did not distribute shards: %+v", counts)
	}
	if owner := RendezvousOwner(1631, zones); owner.ZoneID != "zone-a" {
		t.Fatalf("assignment V1 vector shard 1631 = %s, want zone-a", owner.ZoneID)
	}
	if owner := RendezvousOwner(2066, zones); owner.ZoneID != "zone-b" {
		t.Fatalf("assignment V1 vector shard 2066 = %s, want zone-b", owner.ZoneID)
	}
}

func TestRendezvousAdditionAndRemovalMinimizeRemapping(t *testing.T) {
	base := []ZoneCandidate{
		{ZoneID: "zone-a", Endpoint: "http://127.0.0.1:8082"},
		{ZoneID: "zone-b", Endpoint: "http://127.0.0.1:8084"},
	}
	expanded := append(append([]ZoneCandidate(nil), base...),
		ZoneCandidate{ZoneID: "zone-c", Endpoint: "http://127.0.0.1:8085"})
	for shardID := uint32(0); shardID < ShardCount; shardID++ {
		before := RendezvousOwner(shardID, base)
		after := RendezvousOwner(shardID, expanded)
		if after.ZoneID != before.ZoneID && after.ZoneID != "zone-c" {
			t.Fatalf("adding zone-c moved shard %d from %s to existing %s",
				shardID, before.ZoneID, after.ZoneID)
		}
		withoutB := []ZoneCandidate{expanded[0], expanded[2]}
		removed := RendezvousOwner(shardID, withoutB)
		if after.ZoneID != "zone-b" && removed.ZoneID != after.ZoneID {
			t.Fatalf("removing zone-b moved shard %d owned by %s to %s",
				shardID, after.ZoneID, removed.ZoneID)
		}
	}
}

func TestStaticMapRejectsInvalidCandidates(t *testing.T) {
	now := time.Now().UTC()
	for _, zones := range [][]ZoneCandidate{
		nil,
		{{ZoneID: "", Endpoint: "http://127.0.0.1:8082"}},
		{{ZoneID: "zone-a", Endpoint: ""}},
		{
			{ZoneID: "zone-a", Endpoint: "http://127.0.0.1:8082"},
			{ZoneID: "zone-a", Endpoint: "http://127.0.0.1:8084"},
		},
	} {
		if _, err := NewStaticMap(now, time.Minute, zones); err == nil {
			t.Fatalf("NewStaticMap accepted invalid candidates: %+v", zones)
		}
	}
}

func TestStableHashAndShardAreVersionOneConstants(t *testing.T) {
	tests := []struct {
		playerID uint64
		hash     uint64
		shard    uint32
	}{
		{playerID: 0, hash: 12161962213042174405, shard: 2501},
		{playerID: 1, hash: 12161961113530546194, shard: 2066},
		{playerID: 42, hash: 12161933625739840919, shard: 3479},
		{playerID: ^uint64(0), hash: 10157053723145373757, shard: 2109},
	}
	for _, test := range tests {
		if got := StableHash64(test.playerID); got != test.hash {
			t.Errorf("StableHash64(%d) = %d, want %d", test.playerID, got, test.hash)
		}
		if got := ShardForPlayer(test.playerID); got >= ShardCount {
			t.Errorf("ShardForPlayer(%d) = %d, want < %d", test.playerID, got, ShardCount)
		} else if got != test.shard {
			t.Errorf("ShardForPlayer(%d) = %d, want %d", test.playerID, got, test.shard)
		}
	}
}

func TestNewMapFromSnapshotRestoresExactCurrent(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	original, err := NewStaticMap(now, time.Minute, []ZoneCandidate{{
		ZoneID: "zone-a", Endpoint: "http://zone-a:8082",
	}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := original.Snapshot()
	snapshot.MapVersion = 17
	snapshot.CommittedIndex = 17
	snapshot.Entries[42].OwnerEpoch = 9
	snapshot.Entries[42].RouteVersion = 13
	snapshot.Entries[42].LeaseID = "00112233-4455-6677-8899-aabbccddeeff"
	restored, err := NewMapFromSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	got := restored.Snapshot()
	if got.MapVersion != 17 || got.Entries[42] != snapshot.Entries[42] {
		t.Fatalf("restored snapshot changed current: %+v", got.Entries[42])
	}
}

func TestNewMapFromSnapshotRejectsMalformedCurrent(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	original, err := NewLocalMap(now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Snapshot){
		"incomplete": func(snapshot *Snapshot) { snapshot.Entries = snapshot.Entries[:ShardCount-1] },
		"unordered":  func(snapshot *Snapshot) { snapshot.Entries[42].ShardID = 43 },
		"zero epoch": func(snapshot *Snapshot) { snapshot.Entries[42].OwnerEpoch = 0 },
		"bad state":  func(snapshot *Snapshot) { snapshot.Entries[42].State = "BROKEN" },
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := original.Snapshot()
			mutate(&snapshot)
			if _, err := NewMapFromSnapshot(snapshot); err == nil {
				t.Fatal("malformed snapshot accepted")
			}
		})
	}
}

func TestLegacyLeaseRenewalStillAdvancesVersions(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	routes, _ := NewLocalMap(now, 30*time.Second)
	before := routes.Snapshot()
	renewed, err := routes.RenewOwnedLeases(DefaultZoneID, now.Add(10*time.Second), 30*time.Second)
	if err != nil || renewed != int(ShardCount) {
		t.Fatalf("renewed=%d err=%v", renewed, err)
	}
	after := routes.Snapshot()
	if after.MapVersion != before.MapVersion+1 ||
		after.Entries[42].RouteVersion != before.Entries[42].RouteVersion+1 {
		t.Fatalf("legacy renewal versions map=%d route=%d", after.MapVersion, after.Entries[42].RouteVersion)
	}
}

func TestMapProposalsDoNotMutateUntilCommittedSnapshotApplied(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	routes, _ := NewLocalMap(now, time.Minute)
	before := routes.Snapshot()
	prepared, err := routes.ProposePrepare(42, "zone-next", "http://zone-next:8082", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got := routes.Snapshot(); got.MapVersion != before.MapVersion || got.Entries[42] != before.Entries[42] {
		t.Fatal("ProposePrepare mutated Current")
	}
	committed := before
	committed.MapVersion++
	committed.CommittedIndex++
	committed.Entries = append([]RouteEntry(nil), before.Entries...)
	committed.Entries[42] = prepared
	if err := routes.ApplyCommittedSnapshot(committed); err != nil {
		t.Fatal(err)
	}
	if got := routes.Snapshot(); got.Entries[42] != prepared {
		t.Fatalf("committed PREPARING not applied: %+v", got.Entries[42])
	}
	if err := routes.ApplyCommittedSnapshot(before); err == nil {
		t.Fatal("map version rollback accepted")
	}
}

func TestOnlyActiveUnexpiredExactOwnerIsAccepted(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	routes, err := NewLocalMap(now, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	if err := routes.ValidateOwner(17, DefaultZoneID, 1, now); err != nil {
		t.Fatalf("valid owner rejected: %v", err)
	}
	for name, validate := range map[string]func() error{
		"wrong zone":  func() error { return routes.ValidateOwner(17, "zone-stale", 1, now) },
		"wrong epoch": func() error { return routes.ValidateOwner(17, DefaultZoneID, 2, now) },
		"expired":     func() error { return routes.ValidateOwner(17, DefaultZoneID, 1, now.Add(30*time.Second)) },
	} {
		t.Run(name, func(t *testing.T) {
			err := validate()
			if !IsNotOwner(err) {
				t.Fatalf("error = %v, want NOT_OWNER", err)
			}
			var notOwner *NotOwnerError
			if !errors.As(err, &notOwner) || notOwner.Code != "NOT_OWNER" {
				t.Fatalf("error does not expose NOT_OWNER metadata: %v", err)
			}
		})
	}
}

func TestTransitionConsumesEpochAndPreparingIsNotRoutable(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	routes, err := NewLocalMap(now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	preparing, err := routes.Prepare(23, "zone-next", "http://127.0.0.1:9082", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if preparing.State != RouteStatePreparing || preparing.OwnerEpoch != 2 ||
		preparing.RouteVersion != 2 || preparing.TransitionID == "" {
		t.Fatalf("unexpected preparing route: %+v", preparing)
	}
	if _, err := routes.Route(23, now); !IsNotOwner(err) {
		t.Fatalf("PREPARING route error = %v, want NOT_OWNER", err)
	}
	if err := routes.ValidateOwner(23, "zone-next", 2, now); !IsNotOwner(err) {
		t.Fatalf("PREPARING owner validation = %v, want NOT_OWNER", err)
	}
	if renewed, err := routes.RenewOwnedLeases(
		"zone-next", now.Add(500*time.Millisecond), time.Minute,
	); err != nil || renewed != 0 {
		t.Fatalf("PREPARING lease renewal = %d, %v; want 0, nil", renewed, err)
	}
	stillPreparing, err := routes.Entry(23)
	if err != nil {
		t.Fatal(err)
	}
	if stillPreparing.RouteVersion != preparing.RouteVersion ||
		stillPreparing.LeaseExpiresAt != preparing.LeaseExpiresAt {
		t.Fatalf("PREPARING route changed during lease renewal: %+v", stillPreparing)
	}

	active, err := routes.Activate(23, preparing.TransitionID, now.Add(time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if active.State != RouteStateActive || active.OwnerEpoch != 2 || active.RouteVersion != 3 {
		t.Fatalf("unexpected active route: %+v", active)
	}
	if err := routes.ValidateOwner(23, "zone-next", 2, now.Add(time.Second)); err != nil {
		t.Fatalf("new owner rejected: %v", err)
	}
	if err := routes.ValidateOwner(23, DefaultZoneID, 1, now.Add(time.Second)); !IsNotOwner(err) {
		t.Fatalf("stale owner error = %v, want NOT_OWNER", err)
	}

	unassigned, err := routes.Unassign(23, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if unassigned.State != RouteStateUnassigned || unassigned.OwnerEpoch != 2 {
		t.Fatalf("unexpected unassigned route: %+v", unassigned)
	}
	next, err := routes.Prepare(23, "zone-third", "http://127.0.0.1:10082", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if next.OwnerEpoch != 3 {
		t.Fatalf("next owner epoch = %d, want 3", next.OwnerEpoch)
	}
}

func TestActivateRequiresExactPreparedTransition(t *testing.T) {
	now := time.Now().UTC()
	routes, err := NewLocalMap(now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	preparing, err := routes.Prepare(1, "zone-next", "http://127.0.0.1:9082", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := routes.Activate(1, "wrong-transition", now, time.Minute); err == nil {
		t.Fatal("Activate accepted the wrong transition")
	}
	active, err := routes.Activate(1, preparing.TransitionID, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if active.OwnerEpoch != preparing.OwnerEpoch {
		t.Fatalf("Activate changed epoch from %d to %d", preparing.OwnerEpoch, active.OwnerEpoch)
	}
}

func TestRenewOwnedLeasesCommitsVersions(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	routes, err := NewLocalMap(now, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	before := routes.Snapshot()
	renewed, err := routes.RenewOwnedLeases(DefaultZoneID, now.Add(10*time.Second), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if renewed != int(ShardCount) {
		t.Fatalf("renewed = %d, want %d", renewed, ShardCount)
	}
	after := routes.Snapshot()
	if after.MapVersion != before.MapVersion+1 ||
		after.CommittedIndex != before.CommittedIndex+1 {
		t.Fatalf("batch renewal did not commit once: before=%+v after=%+v", before, after)
	}
	entry := after.Entries[0]
	if entry.RouteVersion != 2 ||
		!entry.LeaseExpiresAt.Equal(now.Add(40*time.Second)) {
		t.Fatalf("unexpected renewed route: %+v", entry)
	}
}
