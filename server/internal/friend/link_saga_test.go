package friend

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/testtcaplus"
)

// stubCreditor is a TaskCreditor test double that tracks how many times each
// player was credited so tests can assert idempotency.
type stubCreditor struct {
	mu      sync.Mutex
	calls   map[uint64]int
	failFor map[uint64]int
	nextSeq uint64
}

func newStubCreditor() *stubCreditor {
	return &stubCreditor{calls: make(map[uint64]int), failFor: make(map[uint64]int)}
}

func (s *stubCreditor) ApplyFriendTaskCredit(
	_ context.Context, playerID uint64, _ []byte,
) (bool, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failFor[playerID] > 0 {
		s.failFor[playerID]--
		return false, 0, errNoResponse
	}
	s.calls[playerID]++
	s.nextSeq++
	return true, s.nextSeq, nil
}

func (s *stubCreditor) callCount(playerID uint64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[playerID]
}

var errNoResponse = errTest("stub credit failure")

type errTest string

func (e errTest) Error() string { return string(e) }

func seedAccount(t *testing.T, client *testtcaplus.Client, store *TcaplusStore, playerID uint64, name string) {
	t.Helper()
	account := &tcaplusv1.AccountByPlayer{
		PlayerId: playerID, AccountName: name, Status: 3, CreatedAtMs: 1, UpdatedAtMs: 1,
	}
	if err := client.DoInsert(account, insertOpt(context.Background()), 1); err != nil {
		t.Fatalf("seed account %d: %v", playerID, err)
	}
	_ = store
}

func newTestLinker(t *testing.T) (*testtcaplus.Client, *TcaplusStore, *stubCreditor, *FriendLinker) {
	t.Helper()
	client := testtcaplus.New()
	store, err := NewTcaplusStore(client, 1)
	if err != nil {
		t.Fatalf("NewTcaplusStore: %v", err)
	}
	creditor := newStubCreditor()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	linker, err := NewFriendLinker(store, creditor, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewFriendLinker: %v", err)
	}
	return client, store, creditor, linker
}

func TestEstablishFriendshipHappyPath(t *testing.T) {
	client, _, creditor, linker := newTestLinker(t)
	seedAccount(t, client, nil, 1, "alice")
	seedAccount(t, client, nil, 2, "bob")

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	relationID, newlyCreated, err := linker.EstablishFriendship(context.Background(), 1, 2, "ABCD1234", now)
	if err != nil {
		t.Fatalf("EstablishFriendship: %v", err)
	}
	if !newlyCreated {
		t.Fatalf("expected newlyCreated=true")
	}
	if len(relationID) != 16 {
		t.Fatalf("expected 16-byte relation ID, got %d bytes", len(relationID))
	}
	if creditor.callCount(1) != 1 || creditor.callCount(2) != 1 {
		t.Fatalf("expected exactly one credit call per side, got low=%d high=%d",
			creditor.callCount(1), creditor.callCount(2))
	}

	relation, _, err := linker.store.GetRelation(context.Background(), 1, 2)
	if err != nil {
		t.Fatalf("GetRelation: %v", err)
	}
	if relation.Status != tcaplusv1.FriendRelationStatus_FRIEND_RELATION_STATUS_ACTIVE {
		t.Fatalf("expected ACTIVE relation, got %v", relation.Status)
	}

	saga, _, err := linker.store.GetSaga(context.Background(), linkID("ABCD1234", 2))
	if err != nil {
		t.Fatalf("GetSaga: %v", err)
	}
	if saga.Status != tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_COMPLETED {
		t.Fatalf("expected COMPLETED saga, got %v", saga.Status)
	}

	lowList, _, err := linker.store.GetFriendList(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetFriendList(1): %v", err)
	}
	if lowList.ActiveCount != 1 || len(lowList.Entries) != 1 || lowList.Entries[0].FriendPlayerId != 2 {
		t.Fatalf("unexpected low friend list: %+v", lowList)
	}
	if lowList.Entries[0].AccountName != "bob" {
		t.Fatalf("expected friend account name bob, got %q", lowList.Entries[0].AccountName)
	}

	highList, _, err := linker.store.GetFriendList(context.Background(), 2)
	if err != nil {
		t.Fatalf("GetFriendList(2): %v", err)
	}
	if highList.ActiveCount != 1 || len(highList.Entries) != 1 || highList.Entries[0].FriendPlayerId != 1 {
		t.Fatalf("unexpected high friend list: %+v", highList)
	}
}

func TestEstablishFriendshipRepeatIsIdempotent(t *testing.T) {
	client, _, creditor, linker := newTestLinker(t)
	seedAccount(t, client, nil, 1, "alice")
	seedAccount(t, client, nil, 2, "bob")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	first, firstNew, err := linker.EstablishFriendship(context.Background(), 1, 2, "ABCD1234", now)
	if err != nil {
		t.Fatalf("first EstablishFriendship: %v", err)
	}
	if !firstNew {
		t.Fatalf("expected first call newlyCreated=true")
	}

	second, secondNew, err := linker.EstablishFriendship(context.Background(), 1, 2, "ABCD1234", now)
	if err != nil {
		t.Fatalf("second EstablishFriendship: %v", err)
	}
	if secondNew {
		t.Fatalf("expected repeat redeem newlyCreated=false")
	}
	if string(second) != string(first) {
		t.Fatalf("expected same relation ID on repeat redeem")
	}
	if creditor.callCount(1) != 1 || creditor.callCount(2) != 1 {
		t.Fatalf("expected credit applied exactly once per side even on retry, got low=%d high=%d",
			creditor.callCount(1), creditor.callCount(2))
	}

	list, _, err := linker.store.GetFriendList(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetFriendList: %v", err)
	}
	if list.ActiveCount != 1 || list.ReservedCount != 0 {
		t.Fatalf("expected no duplicate reservation/entry, got %+v", list)
	}
}

func TestEstablishFriendshipCannotFriendSelf(t *testing.T) {
	_, _, _, linker := newTestLinker(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, _, err := linker.EstablishFriendship(context.Background(), 1, 1, "ABCD1234", now)
	if err != ErrCannotFriendSelf {
		t.Fatalf("expected ErrCannotFriendSelf, got %v", err)
	}
}

func TestEstablishFriendshipLimitReached(t *testing.T) {
	client, _, _, linker := newTestLinker(t)
	seedAccount(t, client, nil, 1, "alice")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := uint64(2); i < 2+maxFriendsPerPlayer; i++ {
		seedAccount(t, client, nil, i, "friend")
		if _, _, err := linker.EstablishFriendship(context.Background(), 1, i, codeFor(i), now); err != nil {
			t.Fatalf("EstablishFriendship(%d) at friend #%d: %v", i, i-1, err)
		}
	}

	overflowID := uint64(2 + maxFriendsPerPlayer)
	seedAccount(t, client, nil, overflowID, "overflow")
	_, _, err := linker.EstablishFriendship(context.Background(), 1, overflowID, codeFor(overflowID), now)
	if err != ErrFriendLimitReached {
		t.Fatalf("expected ErrFriendLimitReached at friend #101, got %v", err)
	}

	list, _, err := linker.store.GetFriendList(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetFriendList: %v", err)
	}
	if list.ActiveCount != maxFriendsPerPlayer {
		t.Fatalf("expected exactly %d active friends, got %d", maxFriendsPerPlayer, list.ActiveCount)
	}
	if list.ReservedCount != 0 {
		t.Fatalf("expected the rejected reservation to be released, got reserved_count=%d", list.ReservedCount)
	}

	saga, _, err := linker.store.GetSaga(context.Background(), linkID(codeFor(overflowID), overflowID))
	if err != nil {
		t.Fatalf("GetSaga: %v", err)
	}
	if saga.Status != tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_ABORTED {
		t.Fatalf("expected ABORTED saga for the rejected friend, got %v", saga.Status)
	}
}

func codeFor(playerID uint64) string {
	return fmt.Sprintf("C%07d", playerID)
}

func TestEstablishFriendshipTaskCreditRetriesWithoutDoubleCrediting(t *testing.T) {
	client, _, creditor, linker := newTestLinker(t)
	seedAccount(t, client, nil, 1, "alice")
	seedAccount(t, client, nil, 2, "bob")
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	creditor.failFor[2] = 1
	relationID, newlyCreated, err := linker.EstablishFriendship(context.Background(), 1, 2, "ABCD1234", now)
	if err != nil {
		t.Fatalf("first EstablishFriendship should not fail the caller: %v", err)
	}
	if !newlyCreated {
		t.Fatalf("expected newlyCreated=true even while task credit is pending")
	}
	if len(relationID) != 16 {
		t.Fatalf("expected relation ID even while task credit is pending")
	}

	saga, version, err := linker.store.GetSaga(context.Background(), linkID("ABCD1234", 2))
	if err != nil {
		t.Fatalf("GetSaga: %v", err)
	}
	if saga.Status != tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_TASK_CREDITING {
		t.Fatalf("expected saga stuck at TASK_CREDITING, got %v", saga.Status)
	}
	if saga.LowTaskCreditStatus != tcaplusv1.FriendTaskCreditStatus_FRIEND_TASK_CREDIT_STATUS_APPLIED {
		t.Fatalf("expected low side already credited, got %v", saga.LowTaskCreditStatus)
	}
	if creditor.callCount(1) != 1 {
		t.Fatalf("expected exactly one credit call for the low side, got %d", creditor.callCount(1))
	}

	reconciler, err := NewReconciler(linker.store, linker, client)
	if err != nil {
		t.Fatalf("NewReconciler: %v", err)
	}
	if err := reconciler.ReconcileSaga(context.Background(), saga, now.Add(time.Minute)); err != nil {
		t.Fatalf("ReconcileSaga: %v", err)
	}

	final, _, err := linker.store.GetSaga(context.Background(), linkID("ABCD1234", 2))
	if err != nil {
		t.Fatalf("GetSaga after reconcile: %v", err)
	}
	if final.Status != tcaplusv1.FriendLinkSagaStatus_FRIEND_LINK_SAGA_STATUS_COMPLETED {
		t.Fatalf("expected COMPLETED saga after reconcile, got %v", final.Status)
	}
	if creditor.callCount(1) != 1 {
		t.Fatalf("expected low side credited exactly once total, got %d", creditor.callCount(1))
	}
	if creditor.callCount(2) != 1 {
		t.Fatalf("expected high side credited exactly once total, got %d", creditor.callCount(2))
	}
	_ = version
}
