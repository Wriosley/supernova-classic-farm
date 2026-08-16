package connection

import (
	"sync"
	"testing"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func TestRegisterRefreshIdempotent(t *testing.T) {
	reg := NewRegistry()
	now := time.Unix(1000, 0)
	conn := PlayerConnection{
		PlayerID: 7, GateID: "gate-a", ConnectionID: "c1", ExpiresAt: now.Add(LeaseTTL),
	}
	if err := reg.Register(conn); err != nil {
		t.Fatal(err)
	}
	later := now.Add(30 * time.Second)
	if err := reg.Register(PlayerConnection{
		PlayerID: 7, GateID: "gate-a", ConnectionID: "c1", ExpiresAt: later.Add(LeaseTTL),
	}); err != nil {
		t.Fatal(err)
	}
	list := reg.List(7)
	if len(list) != 1 {
		t.Fatalf("List = %+v", list)
	}
	if !list[0].ExpiresAt.Equal(later.Add(LeaseTTL)) {
		t.Fatalf("expires = %v", list[0].ExpiresAt)
	}
	if err := reg.Refresh(7, "gate-a", "c1", later.Add(2*LeaseTTL)); err != nil {
		t.Fatal(err)
	}
	if got := reg.List(7)[0].ExpiresAt; !got.Equal(later.Add(2 * LeaseTTL)) {
		t.Fatalf("refresh expires = %v", got)
	}
}

func TestRegisterRejectsGateEndpointMutation(t *testing.T) {
	reg := NewRegistry()
	first := PlayerConnection{PlayerID: 7, GateID: "pod-uid", GateEndpoint: "http://gate-0.gate-headless.classic-farm.svc.cluster.local:8081", ConnectionID: "c1", ExpiresAt: time.Now().Add(LeaseTTL)}
	if err := reg.Register(first); err != nil {
		t.Fatal(err)
	}
	first.GateEndpoint = "http://gate-1.gate-headless.classic-farm.svc.cluster.local:8081"
	if err := reg.Register(first); err != ErrConnectionMismatch {
		t.Fatalf("Register endpoint mutation = %v, want ErrConnectionMismatch", err)
	}
}

func TestRegisterRejectsNonGateEndpoint(t *testing.T) {
	err := NewRegistry().Register(PlayerConnection{PlayerID: 7, GateID: "pod-uid", GateEndpoint: "http://gate:8081", ConnectionID: "c1", ExpiresAt: time.Now().Add(LeaseTTL)})
	if err != ErrInvalidConnection {
		t.Fatalf("Register arbitrary service endpoint = %v", err)
	}
}

func TestUnregisterIgnoresStaleConnectionID(t *testing.T) {
	reg := NewRegistry()
	now := time.Unix(1000, 0)
	if err := reg.Register(PlayerConnection{
		PlayerID: 1, GateID: "gate-a", ConnectionID: "old", ExpiresAt: now.Add(LeaseTTL),
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(PlayerConnection{
		PlayerID: 1, GateID: "gate-a", ConnectionID: "new", ExpiresAt: now.Add(LeaseTTL),
	}); err != nil {
		t.Fatal(err)
	}
	// New registration coexists; unregister old must not remove new.
	reg.Unregister(1, "gate-a", "old")
	list := reg.List(1)
	if len(list) != 1 || list[0].ConnectionID != "new" {
		t.Fatalf("after stale unregister = %+v", list)
	}
	reg.Unregister(1, "gate-a", "new")
	if got := reg.List(1); len(got) != 0 {
		t.Fatalf("after matching unregister = %+v", got)
	}
}

func TestListAllowsMultipleGatesAndSorts(t *testing.T) {
	reg := NewRegistry()
	now := time.Unix(1000, 0)
	for _, conn := range []PlayerConnection{
		{PlayerID: 9, GateID: "gate-b", ConnectionID: "c2", ExpiresAt: now.Add(LeaseTTL)},
		{PlayerID: 9, GateID: "gate-a", ConnectionID: "c2", ExpiresAt: now.Add(LeaseTTL)},
		{PlayerID: 9, GateID: "gate-a", ConnectionID: "c1", ExpiresAt: now.Add(LeaseTTL)},
	} {
		if err := reg.Register(conn); err != nil {
			t.Fatal(err)
		}
	}
	list := reg.List(9)
	if len(list) != 3 {
		t.Fatalf("len=%d", len(list))
	}
	want := [][2]string{{"gate-a", "c1"}, {"gate-a", "c2"}, {"gate-b", "c2"}}
	for i, pair := range want {
		if list[i].GateID != pair[0] || list[i].ConnectionID != pair[1] {
			t.Fatalf("list[%d]=%+v want %v", i, list[i], pair)
		}
	}
}

func TestEvictExpired(t *testing.T) {
	reg := NewRegistry()
	now := time.Unix(1000, 0)
	_ = reg.Register(PlayerConnection{
		PlayerID: 1, GateID: "g", ConnectionID: "alive", ExpiresAt: now.Add(LeaseTTL),
	})
	_ = reg.Register(PlayerConnection{
		PlayerID: 2, GateID: "g", ConnectionID: "dead", ExpiresAt: now.Add(time.Second),
	})
	removed := reg.EvictExpired(now.Add(2 * time.Second))
	if len(removed) != 1 || removed[0].PlayerID != 2 {
		t.Fatalf("removed=%+v", removed)
	}
	if len(reg.List(1)) != 1 || len(reg.List(2)) != 0 {
		t.Fatalf("alive=%v dead=%v", reg.List(1), reg.List(2))
	}
}

func TestRemoveShard(t *testing.T) {
	reg := NewRegistry()
	now := time.Unix(1000, 0)
	var target uint64 = 1
	for playerID := uint64(1); playerID < 64; playerID++ {
		if routing.ShardForPlayer(playerID) != routing.ShardForPlayer(target) {
			continue
		}
		_ = reg.Register(PlayerConnection{
			PlayerID: playerID, GateID: "g", ConnectionID: "c", ExpiresAt: now.Add(LeaseTTL),
		})
		break
	}
	other := uint64(0)
	for playerID := uint64(1); playerID < 256; playerID++ {
		if routing.ShardForPlayer(playerID) != routing.ShardForPlayer(target) {
			other = playerID
			_ = reg.Register(PlayerConnection{
				PlayerID: playerID, GateID: "g", ConnectionID: "c", ExpiresAt: now.Add(LeaseTTL),
			})
			break
		}
	}
	if other == 0 {
		t.Fatal("could not find other-shard player")
	}
	// Find the registered same-shard player.
	var same uint64
	for playerID := uint64(1); playerID < 256; playerID++ {
		if routing.ShardForPlayer(playerID) == routing.ShardForPlayer(target) && len(reg.List(playerID)) > 0 {
			same = playerID
			break
		}
	}
	removed := reg.RemoveShard(routing.ShardForPlayer(same))
	if len(removed) == 0 {
		t.Fatal("expected removals")
	}
	if len(reg.List(same)) != 0 {
		t.Fatalf("same-shard still present")
	}
	if len(reg.List(other)) == 0 {
		t.Fatalf("other-shard should remain")
	}
}

func TestRefreshMissingReturnsMismatch(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Refresh(1, "g", "c", time.Now().Add(LeaseTTL)); err != ErrConnectionMismatch {
		t.Fatalf("err=%v", err)
	}
}

func TestRegistryConcurrentRegisterRefresh(t *testing.T) {
	reg := NewRegistry()
	now := time.Unix(1000, 0)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			connID := "c0"
			if i%2 == 0 {
				connID = "c1"
			}
			_ = reg.Register(PlayerConnection{
				PlayerID: 42, GateID: "gate", ConnectionID: connID, ExpiresAt: now.Add(LeaseTTL),
			})
			_ = reg.Refresh(42, "gate", connID, now.Add(2*LeaseTTL))
			_ = reg.List(42)
			if i%5 == 0 {
				reg.Unregister(42, "gate", "missing")
			}
		}(i)
	}
	wg.Wait()
	list := reg.List(42)
	if len(list) == 0 || len(list) > 2 {
		t.Fatalf("list=%+v", list)
	}
}
