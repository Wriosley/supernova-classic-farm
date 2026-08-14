package player

import (
	"context"
	"sync"
	"testing"
	"time"
)

type presenceMap map[uint64]bool

func (p presenceMap) Has(playerID uint64) bool { return p[playerID] }

type observerMap map[uint64]bool

func (o observerMap) HasVisitors(ownerPlayerID uint64, _ time.Time) bool {
	return o[ownerPlayerID]
}

type evictionStore struct {
	mu       sync.Mutex
	state    *State
	token    StoreToken
	failNext int
	saves    int
}

func (s *evictionStore) Load(_ context.Context, playerID uint64) (LoadedCheckpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil || s.state.PlayerID != playerID {
		return LoadedCheckpoint{}, ErrCheckpointNotFound
	}
	clone := *s.state
	return LoadedCheckpoint{
		State:             &clone,
		PersistedRevision: s.state.CheckpointRevision,
		Token:             cloneStoreToken(s.token),
	}, nil
}

func (s *evictionStore) SaveCAS(_ context.Context, write CheckpointWrite) (CheckpointWriteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves++
	if s.failNext > 0 {
		s.failNext--
		return CheckpointWriteResult{Status: CheckpointWriteRetryableFailure}, nil
	}
	state, err := StateFromCheckpoint(write.Checkpoint)
	if err != nil {
		return CheckpointWriteResult{}, err
	}
	s.state = state
	s.token = StoreToken("evicted-token")
	return CheckpointWriteResult{
		Status:   CheckpointWriteApplied,
		NewToken: cloneStoreToken(s.token),
	}, nil
}

func activateIdleActor(t *testing.T, runtime *Runtime, playerID uint64, now time.Time) *runtimeActor {
	t.Helper()
	runtime.SetNow(func() time.Time { return now })
	if _, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		snapshotRequest(playerID, "activate-idle")); err != nil {
		t.Fatalf("activate: %v", err)
	}
	runtime.mu.Lock()
	a := runtime.actors[playerID]
	runtime.mu.Unlock()
	if a == nil {
		t.Fatal("actor missing after activate")
	}
	return a
}

func TestEvictIdleActorBlockedByConnection(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	runtime := NewRuntime()
	defer runtime.Close()
	runtime.SetPlayerPresence(presenceMap{42: true})
	a := activateIdleActor(t, runtime, 42, now)
	a.touchAccess(now.Add(-actorIdleTimeout - time.Second))
	if err := runtime.EvictIdleActors(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	_, ok := runtime.actors[42]
	runtime.mu.Unlock()
	if !ok {
		t.Fatal("connected owner must not be evicted")
	}
}

func TestEvictIdleActorBlockedByVisitors(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	runtime := NewRuntime()
	defer runtime.Close()
	runtime.SetFarmObservers(observerMap{42: true})
	a := activateIdleActor(t, runtime, 42, now)
	a.touchAccess(now.Add(-actorIdleTimeout - time.Second))
	if err := runtime.EvictIdleActors(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if runtime.actors[42] == nil {
		t.Fatal("owner with visitors must not be evicted")
	}
}

func TestEvictIdleActorBlockedByMailboxWork(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	runtime := NewRuntime()
	defer runtime.Close()
	a := activateIdleActor(t, runtime, 42, now)
	a.touchAccess(now.Add(-actorIdleTimeout - time.Second))

	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- a.mailbox.Do(context.Background(), func() {
			close(started)
			<-release
		})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("mailbox work did not start")
	}
	if err := runtime.EvictIdleActors(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if runtime.actors[42] == nil {
		t.Fatal("busy mailbox must block eviction")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestEvictIdleActorBlockedByRecentAccess(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	runtime := NewRuntime()
	defer runtime.Close()
	a := activateIdleActor(t, runtime, 42, now)
	a.touchAccess(now.Add(-actorIdleTimeout + time.Second))
	if err := runtime.EvictIdleActors(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if runtime.actors[42] == nil {
		t.Fatal("recent access must block eviction")
	}
}

func TestEvictIdleActorSaveCASBeforeDelete(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	state := NewDevelopmentState(42)
	state.CreatedAtMS = now.UnixMilli()
	state.UpdatedAtMS = now.UnixMilli()
	state.CheckpointRevision = 2
	state.PlayerSeq = 2
	store := &evictionStore{state: state, token: StoreToken("tok")}
	runtime, err := NewRuntimeWithStore(store)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	a := activateIdleActor(t, runtime, 42, now)
	a.touchAccess(now.Add(-actorIdleTimeout - time.Second))

	// Force a dirty revision that eviction must flush.
	if err := a.mailbox.Do(context.Background(), func() {
		a.state.PlayerSeq++
		a.state.CheckpointRevision++
		a.state.UpdatedAtMS = now.UnixMilli()
	}); err != nil {
		t.Fatal(err)
	}
	runtime.markDirty(42, a.state.CheckpointRevision)

	if err := runtime.EvictIdleActors(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if store.saves == 0 {
		t.Fatal("expected SaveCAS before delete")
	}
	deadline := time.Now().Add(time.Second)
	for {
		runtime.mu.Lock()
		_, ok := runtime.actors[42]
		runtime.mu.Unlock()
		if !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actor was not removed after successful eviction")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestEvictIdleActorSaveCASFailureRetainsActor(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	state := NewDevelopmentState(42)
	state.CreatedAtMS = now.UnixMilli()
	state.UpdatedAtMS = now.UnixMilli()
	state.CheckpointRevision = 2
	state.PlayerSeq = 2
	store := &evictionStore{state: state, token: StoreToken("tok"), failNext: 1}
	runtime, err := NewRuntimeWithStore(store)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	a := activateIdleActor(t, runtime, 42, now)
	a.touchAccess(now.Add(-actorIdleTimeout - time.Second))
	if err := a.mailbox.Do(context.Background(), func() {
		a.state.PlayerSeq++
		a.state.CheckpointRevision++
		a.state.UpdatedAtMS = now.UnixMilli()
	}); err != nil {
		t.Fatal(err)
	}
	runtime.markDirty(42, a.state.CheckpointRevision)

	err = runtime.EvictIdleActors(context.Background(), now)
	if err == nil {
		t.Fatal("expected flush failure")
	}
	if runtime.actors[42] == nil {
		t.Fatal("flush failure must retain actor")
	}
	if a.lifecycle.Load() != actorLifecycleReady {
		t.Fatalf("lifecycle=%d want Ready", a.lifecycle.Load())
	}
}

func TestEvictIdleActorAccessRaceAborts(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	runtime := NewRuntime()
	defer runtime.Close()
	a := activateIdleActor(t, runtime, 42, now)
	a.touchAccess(now.Add(-actorIdleTimeout - time.Second))

	// Inject a fresher access after eligibility scan by holding the mailbox
	// briefly so EvictIdleActors marks Evicting then rechecks inside Do.
	var once sync.Once
	release := make(chan struct{})
	blocker := make(chan struct{})
	go func() {
		_ = a.mailbox.Do(context.Background(), func() {
			once.Do(func() { close(blocker) })
			a.touchAccess(now)
			<-release
		})
	}()
	select {
	case <-blocker:
	case <-time.After(time.Second):
		t.Fatal("blocker did not start")
	}
	// Busy mailbox blocks the first eligibility check.
	if err := runtime.EvictIdleActors(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	close(release)
	time.Sleep(20 * time.Millisecond)
	if runtime.actors[42] == nil {
		t.Fatal("actor should remain while access is recent")
	}

	// Now idle again with recent access timestamp — still too fresh.
	if err := runtime.EvictIdleActors(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if runtime.actors[42] == nil {
		t.Fatal("recent touchAccess must abort eviction")
	}
}

func TestEvictIdleActorTickDoesNotExtendAccess(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	runtime := NewRuntime()
	defer runtime.Close()
	a := activateIdleActor(t, runtime, 42, now)
	past := now.Add(-actorIdleTimeout - time.Second)
	a.touchAccess(past)
	if err := a.mailbox.Do(context.Background(), func() {
		if _, err := a.tick(now); err != nil {
			t.Errorf("tick: %v", err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !a.lastAccessAt().Equal(past) {
		t.Fatalf("tick extended lastAccessAt to %v", a.lastAccessAt())
	}
}
