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
		if got := ShardForPlayer(test.playerID); got != test.shard {
			t.Errorf("ShardForPlayer(%d) = %d, want %d", test.playerID, got, test.shard)
		}
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
