package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"github.com/Wriosley/supernova-classic-farm/server/internal/testtcaplus"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
)

func TestTcaplusRegistrationSagaAndSessionGeneration(t *testing.T) {
	client := testtcaplus.New()
	// 故意不写入 ShardFence / PlayerCheckpoint：注册不得依赖它们。
	store, err := NewTcaplusStore(client, 1)
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

	var accountByName tcaplusv1.AccountByName
	accountByName.AccountName = "farmer_one"
	if err := client.DoGet(&accountByName, &option.PBOpt{}, 1); err != nil {
		t.Fatalf("AccountByName missing after register: %v", err)
	}
	var accountByPlayer tcaplusv1.AccountByPlayer
	accountByPlayer.PlayerId = session.PlayerID
	if err := client.DoGet(&accountByPlayer, &option.PBOpt{}, 1); err != nil {
		t.Fatalf("AccountByPlayer missing after register: %v", err)
	}
	var counter tcaplusv1.PlayerIdCounter
	counter.CounterId = 1
	if err := client.DoGet(&counter, &option.PBOpt{}, 1); err != nil {
		t.Fatalf("PlayerIdCounter missing after register: %v", err)
	}
	if _, err := store.Session(raw); err != nil {
		t.Fatal(err)
	}

	checkpoints, err := player.NewTcaplusCheckpointStoreWithClient(client, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkpoints.Load(context.Background(), session.PlayerID); !errors.Is(err, player.ErrCheckpointNotFound) {
		t.Fatalf("Load after register = %v, want ErrCheckpointNotFound", err)
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
	store, err := NewTcaplusStore(client, 1)
	if err != nil {
		t.Fatal(err)
	}
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
