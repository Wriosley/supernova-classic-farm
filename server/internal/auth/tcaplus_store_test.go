package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"github.com/Wriosley/supernova-classic-farm/server/internal/testtcaplus"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
)

func TestTcaplusRegistrationSagaAndSessionGeneration(t *testing.T) {
	client := testtcaplus.New()
	shardID := routing.ShardForPlayer(1)
	if err := client.DoInsert(&tcaplusv1.ShardFence{
		LogicalShardId: shardID, OwnerZoneId: "zone-a",
		OwnerEpoch: 1, RouteVersion: 1,
	}, &option.PBOpt{}, 1); err != nil {
		t.Fatal(err)
	}
	checkpoints, err := player.NewTcaplusCheckpointStoreWithClient(client, 1)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewTcaplusStore(client, 1, checkpoints)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 5, 8, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	raw, session, err := store.Register("farmer_one", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if session.PlayerID != 1 || session.Generation != 1 || raw == "" {
		t.Fatalf("registration Session = %+v raw=%q", session, raw)
	}
	loaded, err := checkpoints.Load(context.Background(), session.PlayerID)
	if err != nil || loaded.State.OwnerEpoch != 1 {
		t.Fatalf("initial checkpoint = %+v error=%v", loaded, err)
	}
	if _, err := store.Session(raw); err != nil {
		t.Fatal(err)
	}

	loginRaw, loginSession, err := store.Login(
		"farmer_one", "correct-horse-battery",
	)
	if err != nil {
		t.Fatal(err)
	}
	if loginRaw == "" || loginSession.Generation != 2 {
		t.Fatalf("login Session = %+v raw=%q", loginSession, loginRaw)
	}
	if _, err := store.Session(raw); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("old Session error = %v, want ErrUnauthenticated", err)
	}
	if _, err := store.Session(loginRaw); err != nil {
		t.Fatal(err)
	}
}

func TestTcaplusRegistrationRejectsDuplicateActiveName(t *testing.T) {
	client := testtcaplus.New()
	if err := client.DoInsert(&tcaplusv1.ShardFence{
		LogicalShardId: routing.ShardForPlayer(1), OwnerZoneId: "zone-a",
		OwnerEpoch: 1, RouteVersion: 1,
	}, &option.PBOpt{}, 1); err != nil {
		t.Fatal(err)
	}
	checkpoints, _ := player.NewTcaplusCheckpointStoreWithClient(client, 1)
	store, _ := NewTcaplusStore(client, 1, checkpoints)
	if _, _, err := store.Register(
		"farmer_two", "correct-horse-battery",
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Register(
		"farmer_two", "correct-horse-battery",
	); !errors.Is(err, ErrAccountUnavailable) {
		t.Fatalf("duplicate registration error = %v", err)
	}
}
