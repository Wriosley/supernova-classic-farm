package routing

import (
	"testing"
	"time"
)

func TestAuthorizationTableRejectsWrongZoneEpochShardAndExpiredLease(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	routes, err := NewStaticMap(now, time.Minute, []ZoneCandidate{
		{ZoneID: "zone-a", Endpoint: "http://127.0.0.1:8082"},
		{ZoneID: "zone-b", Endpoint: "http://127.0.0.1:8084"},
	})
	if err != nil {
		t.Fatal(err)
	}
	table, err := NewAuthorizationTable("zone-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := table.Replace(routes.Snapshot()); err != nil {
		t.Fatal(err)
	}

	playerID, shardID := playerOwnedBy(t, routes, "zone-a")
	if err := table.Validate(playerID, shardID, "zone-a", 1, now); err != nil {
		t.Fatalf("valid ownership rejected: %v", err)
	}
	for name, validate := range map[string]func() error{
		"wrong shard": func() error {
			return table.Validate(playerID, (shardID+1)%ShardCount, "zone-a", 1, now)
		},
		"wrong requested zone": func() error {
			return table.Validate(playerID, shardID, "zone-b", 1, now)
		},
		"wrong epoch": func() error {
			return table.Validate(playerID, shardID, "zone-a", 2, now)
		},
		"expired": func() error {
			return table.Validate(playerID, shardID, "zone-a", 1, now.Add(time.Minute))
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validate(); err == nil {
				t.Fatal("invalid ownership accepted")
			}
		})
	}

	otherPlayer, otherShard := playerOwnedBy(t, routes, "zone-b")
	if err := table.Validate(otherPlayer, otherShard, "zone-b", 1, now); !IsNotOwner(err) {
		t.Fatalf("foreign shard error = %v, want NOT_OWNER", err)
	}
}

func TestAuthorizationTableDrainBlocksAndResumeRestoresCommands(t *testing.T) {
	now := time.Date(2026, 8, 3, 14, 0, 0, 0, time.UTC)
	routes, err := NewStaticMap(now, time.Minute, []ZoneCandidate{
		{ZoneID: "zone-a", Endpoint: "http://127.0.0.1:8082"},
		{ZoneID: "zone-b", Endpoint: "http://127.0.0.1:8084"},
	})
	if err != nil {
		t.Fatal(err)
	}
	table, _ := NewAuthorizationTable("zone-a")
	if err := table.Replace(routes.Snapshot()); err != nil {
		t.Fatal(err)
	}
	playerID, shardID := playerOwnedBy(t, routes, "zone-a")
	if _, err := table.BeginDrain(shardID, 1, now); err != nil {
		t.Fatal(err)
	}
	if err := table.Validate(playerID, shardID, "zone-a", 1, now); !IsNotOwner(err) {
		t.Fatalf("draining validation = %v, want NOT_OWNER", err)
	}
	table.Resume(shardID)
	if err := table.Validate(playerID, shardID, "zone-a", 1, now); err != nil {
		t.Fatalf("resumed shard rejected: %v", err)
	}
}

func TestAuthorizationTableClearsOldDrainOnNewerRegrant(t *testing.T) {
	now := time.Date(2026, 8, 3, 14, 0, 0, 0, time.UTC)
	routes, err := NewStaticMap(now, time.Minute, []ZoneCandidate{
		{ZoneID: "zone-a", Endpoint: "http://127.0.0.1:8082"},
		{ZoneID: "zone-b", Endpoint: "http://127.0.0.1:8084"},
	})
	if err != nil {
		t.Fatal(err)
	}
	table, _ := NewAuthorizationTable("zone-a")
	if err := table.Replace(routes.Snapshot()); err != nil {
		t.Fatal(err)
	}
	playerID, shardID := playerOwnedBy(t, routes, "zone-a")
	if _, err := table.BeginDrain(shardID, 1, now); err != nil {
		t.Fatal(err)
	}
	toB, err := routes.Prepare(
		shardID, "zone-b", "http://127.0.0.1:8084", now, time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := routes.Activate(shardID, toB.TransitionID, now, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := table.Replace(routes.Snapshot()); err != nil {
		t.Fatal(err)
	}
	toA, err := routes.Prepare(
		shardID, "zone-a", "http://127.0.0.1:8082", now, time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := routes.Activate(shardID, toA.TransitionID, now, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := table.Replace(routes.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if err := table.Validate(playerID, shardID, "zone-a", 3, now); err != nil {
		t.Fatalf("newer regrant retained old drain: %v", err)
	}
}

func TestAuthorizationTableAcceptsSameVersionExpiryOnlyExtension(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	routes, _ := NewLocalMap(now, 30*time.Second)
	table, _ := NewAuthorizationTable(DefaultZoneID)
	initial := routes.Snapshot()
	if err := table.Replace(initial); err != nil {
		t.Fatal(err)
	}
	extended := initial
	extended.Entries = append([]RouteEntry(nil), initial.Entries...)
	for index := range extended.Entries {
		extended.Entries[index].LeaseExpiresAt = now.Add(time.Minute)
	}
	if err := table.Replace(extended); err != nil {
		t.Fatalf("expiry-only refresh rejected: %v", err)
	}
	got, _ := table.Entry(42)
	if !got.LeaseExpiresAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("expiry-only refresh ignored: %v", got.LeaseExpiresAt)
	}
}

func TestAuthorizationTableRejectsSameVersionOwnershipChanges(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	routes, _ := NewLocalMap(now, time.Minute)
	initial := routes.Snapshot()
	for name, mutate := range map[string]func(*RouteEntry){
		"owner":         func(entry *RouteEntry) { entry.OwnerZoneID = "other" },
		"epoch":         func(entry *RouteEntry) { entry.OwnerEpoch++ },
		"state":         func(entry *RouteEntry) { entry.State = RouteStatePreparing },
		"route version": func(entry *RouteEntry) { entry.RouteVersion++ },
	} {
		t.Run(name, func(t *testing.T) {
			table, _ := NewAuthorizationTable(DefaultZoneID)
			if err := table.Replace(initial); err != nil {
				t.Fatal(err)
			}
			changed := initial
			changed.Entries = append([]RouteEntry(nil), initial.Entries...)
			mutate(&changed.Entries[42])
			if err := table.Replace(changed); err == nil {
				t.Fatal("same-version ownership change accepted")
			}
		})
	}
}

func playerOwnedBy(t *testing.T, routes *Map, zoneID string) (uint64, uint32) {
	t.Helper()
	for playerID := uint64(1); playerID < 100_000; playerID++ {
		shardID := ShardForPlayer(playerID)
		entry, err := routes.Entry(shardID)
		if err != nil {
			t.Fatal(err)
		}
		if entry.OwnerZoneID == zoneID {
			return playerID, shardID
		}
	}
	t.Fatalf("no player found for %s", zoneID)
	return 0, 0
}
