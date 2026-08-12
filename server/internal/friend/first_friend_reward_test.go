package friend

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type stubRewardMailer struct {
	mu    sync.Mutex
	calls []string
	fail  atomic.Bool
}

func (s *stubRewardMailer) CreateSystemRewardMail(
	_ context.Context,
	sourceEventID string,
	_ uint64,
	_, _, _ string,
	_ []RewardMailAttachment,
	_ int64,
) (string, bool, error) {
	if s.fail.Load() {
		return "", false, fmt.Errorf("mail unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.calls {
		if existing == sourceEventID {
			return "mail-dup", true, nil
		}
	}
	s.calls = append(s.calls, sourceEventID)
	return fmt.Sprintf("mail-%d", len(s.calls)), false, nil
}

func (s *stubRewardMailer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func TestFirstFriendRewardsBothPlayers(t *testing.T) {
	client, store, _, _ := newTestLinker(t)
	seedAccount(t, client, store, 1, "alice")
	seedAccount(t, client, store, 2, "bob")
	mailer := &stubRewardMailer{}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	linker, err := NewFriendLinkerWithMailer(store, newStubCreditor(), mailer, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	if _, newly, err := linker.EstablishFriendship(context.Background(), 1, 2, "ABCD1234", now); err != nil {
		t.Fatal(err)
	} else if !newly {
		t.Fatal("expected newly created")
	}
	if mailer.callCount() != 2 {
		t.Fatalf("expected 2 reward mails, got %d", mailer.callCount())
	}
	saga, _, err := store.GetSaga(context.Background(), linkID("ABCD1234", 2))
	if err != nil {
		t.Fatal(err)
	}
	if !saga.FirstRewardClaimed {
		t.Fatal("expected first reward claimed")
	}
}

func TestNonFirstFriendDoesNotRewardEitherPlayer(t *testing.T) {
	client, store, _, _ := newTestLinker(t)
	seedAccount(t, client, store, 1, "alice")
	seedAccount(t, client, store, 2, "bob")
	seedAccount(t, client, store, 3, "carol")
	mailer := &stubRewardMailer{}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	linker, err := NewFriendLinkerWithMailer(store, newStubCreditor(), mailer, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := linker.EstablishFriendship(context.Background(), 1, 2, "CODEAAAA", now); err != nil {
		t.Fatal(err)
	}
	if mailer.callCount() != 2 {
		t.Fatalf("first link should reward once, got %d", mailer.callCount())
	}
	if _, newly, err := linker.EstablishFriendship(context.Background(), 3, 2, "CODEBBBB", now); err != nil {
		t.Fatal(err)
	} else if !newly {
		t.Fatal("second friendship should still create")
	}
	if mailer.callCount() != 2 {
		t.Fatalf("non-first friend must not create more mails, got %d", mailer.callCount())
	}
}

func TestConcurrentFirstFriendClaimsRewardOnce(t *testing.T) {
	client, store, _, _ := newTestLinker(t)
	seedAccount(t, client, store, 10, "invitee")
	seedAccount(t, client, store, 11, "inviterA")
	seedAccount(t, client, store, 12, "inviterB")
	mailer := &stubRewardMailer{}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	linker, err := NewFriendLinkerWithMailer(store, newStubCreditor(), mailer, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _, _ = linker.EstablishFriendship(context.Background(), 11, 10, "CODE1111", now)
	}()
	go func() {
		defer wg.Done()
		_, _, _ = linker.EstablishFriendship(context.Background(), 12, 10, "CODE2222", now)
	}()
	wg.Wait()

	if mailer.callCount() != 2 {
		t.Fatalf("concurrent first friend must reward exactly one pair (2 mails), got %d", mailer.callCount())
	}
}

func TestRewardMailRetryDoesNotDuplicate(t *testing.T) {
	client, store, _, _ := newTestLinker(t)
	seedAccount(t, client, store, 1, "alice")
	seedAccount(t, client, store, 2, "bob")
	mailer := &stubRewardMailer{}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	linker, err := NewFriendLinkerWithMailer(store, newStubCreditor(), mailer, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := linker.EstablishFriendship(context.Background(), 1, 2, "RETRY001", now); err != nil {
		t.Fatal(err)
	}
	if _, newly, err := linker.EstablishFriendship(context.Background(), 1, 2, "RETRY001", now); err != nil {
		t.Fatal(err)
	} else if newly {
		t.Fatal("replay must not report newly_created")
	}
	if mailer.callCount() != 2 {
		t.Fatalf("retry must not duplicate reward mails, got %d", mailer.callCount())
	}
}
