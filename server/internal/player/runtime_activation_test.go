package player

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	"github.com/Wriosley/supernova-classic-farm/server/internal/actor"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

type blockingCheckpointStore struct {
	mu        sync.Mutex
	state     *State
	loadCount atomic.Int64
	started   chan struct{}
	release   chan struct{}
	failErr   error
	rejecting atomic.Bool
}

func newBlockingCheckpointStore(state *State) *blockingCheckpointStore {
	return &blockingCheckpointStore{
		state:   state,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (s *blockingCheckpointStore) Load(ctx context.Context, playerID uint64) (LoadedCheckpoint, error) {
	count := s.loadCount.Add(1)
	if count == 1 {
		close(s.started)
		select {
		case <-s.release:
		case <-ctx.Done():
			return LoadedCheckpoint{}, ctx.Err()
		}
	}
	if s.rejecting.Load() {
		err := s.failErr
		if err == nil {
			err = errors.New("injected load failure")
		}
		return LoadedCheckpoint{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil || s.state.PlayerID != playerID {
		return LoadedCheckpoint{}, ErrCheckpointNotFound
	}
	return LoadedCheckpoint{
		State: s.state, PersistedRevision: s.state.CheckpointRevision,
	}, nil
}

func (s *blockingCheckpointStore) SaveCAS(context.Context, CheckpointWrite) (CheckpointWriteResult, error) {
	return CheckpointWriteResult{Status: CheckpointWriteApplied}, nil
}

func TestRuntimeRegistersActorBeforeCheckpointLoad(t *testing.T) {
	const playerID = uint64(9001)
	const concurrency = 100
	state := NewDevelopmentState(playerID)
	store := newBlockingCheckpointStore(state)
	runtime, err := NewRuntimeWithStore(store)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	var startWG sync.WaitGroup
	startWG.Add(concurrency)
	var doneWG sync.WaitGroup
	doneWG.Add(concurrency)
	actors := make([]*runtimeActor, concurrency)
	errs := make([]error, concurrency)
	began := make(chan struct{})

	for i := 0; i < concurrency; i++ {
		go func(index int) {
			defer doneWG.Done()
			startWG.Done()
			<-began
			actors[index], errs[index] = runtime.actorFor(
				context.Background(), playerID, LocalOwnerEpoch,
			)
		}(i)
	}
	startWG.Wait()
	close(began)
	select {
	case <-store.started:
	case <-time.After(3 * time.Second):
		t.Fatal("Store.Load was not entered")
	}

	runtime.mu.Lock()
	actorCount := len(runtime.actors)
	registered := runtime.actors[playerID]
	runtime.mu.Unlock()
	if actorCount != 1 || registered == nil {
		t.Fatalf("actors during load: count=%d registered=%v", actorCount, registered != nil)
	}
	if store.loadCount.Load() != 1 {
		t.Fatalf("Store.Load calls during blocked activation = %d, want 1", store.loadCount.Load())
	}

	close(store.release)
	doneWG.Wait()

	var first *runtimeActor
	for index, a := range actors {
		if errs[index] != nil {
			t.Fatalf("actorFor[%d] = %v", index, errs[index])
		}
		if a == nil {
			t.Fatalf("actorFor[%d] returned nil actor", index)
		}
		if first == nil {
			first = a
		} else if a != first {
			t.Fatalf("actorFor returned divergent actors")
		}
	}
	if store.loadCount.Load() != 1 {
		t.Fatalf("Store.Load calls after activation = %d, want 1", store.loadCount.Load())
	}
	if first.lifecycle.Load() != actorLifecycleReady || first.mailbox == nil {
		t.Fatalf("activated actor not ready")
	}
}

func TestRuntimeQueuesCommandsBehindActorInitialization(t *testing.T) {
	const playerID = uint64(9002)
	state := NewDevelopmentState(playerID)
	store := newBlockingCheckpointStore(state)
	runtime, err := NewRuntimeWithStore(store)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	var order []string
	var orderMu sync.Mutex
	record := func(label string) {
		orderMu.Lock()
		order = append(order, label)
		orderMu.Unlock()
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		a, err := runtime.actorFor(context.Background(), playerID, LocalOwnerEpoch)
		if err != nil {
			t.Errorf("creator actorFor: %v", err)
			return
		}
		record("creator-ready")
		if err := a.mailbox.Do(context.Background(), func() {
			record("creator-command")
		}); err != nil {
			t.Errorf("creator command: %v", err)
		}
	}()

	select {
	case <-store.started:
	case <-time.After(3 * time.Second):
		t.Fatal("Store.Load was not entered")
	}

	go func() {
		defer wg.Done()
		a, err := runtime.actorFor(context.Background(), playerID, LocalOwnerEpoch)
		if err != nil {
			t.Errorf("waiter actorFor: %v", err)
			return
		}
		record("waiter-ready")
		if err := a.mailbox.Do(context.Background(), func() {
			record("waiter-command")
		}); err != nil {
			t.Errorf("waiter command: %v", err)
		}
	}()

	time.Sleep(50 * time.Millisecond)
	close(store.release)
	wg.Wait()

	orderMu.Lock()
	got := append([]string(nil), order...)
	orderMu.Unlock()
	for index, label := range got {
		if label == "creator-command" || label == "waiter-command" {
			if index == 0 {
				t.Fatalf("business command ran before activation finished: %v", got)
			}
			break
		}
	}
	if store.loadCount.Load() != 1 {
		t.Fatalf("Store.Load calls = %d, want 1", store.loadCount.Load())
	}
}

func TestRuntimeRemovesFailedActivationAndAllowsRetry(t *testing.T) {
	const playerID = uint64(9003)
	state := NewDevelopmentState(playerID)
	store := newBlockingCheckpointStore(state)
	store.rejecting.Store(true)
	store.failErr = errors.New("tcaplus unavailable")
	runtime, err := NewRuntimeWithStore(store)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	const waiters = 8
	var startWG, doneWG sync.WaitGroup
	startWG.Add(waiters)
	doneWG.Add(waiters)
	errs := make([]error, waiters)
	began := make(chan struct{})
	for i := 0; i < waiters; i++ {
		go func(index int) {
			defer doneWG.Done()
			startWG.Done()
			<-began
			_, errs[index] = runtime.actorFor(context.Background(), playerID, LocalOwnerEpoch)
		}(i)
	}
	startWG.Wait()
	close(began)
	select {
	case <-store.started:
	case <-time.After(3 * time.Second):
		t.Fatal("Store.Load was not entered")
	}
	close(store.release)
	doneWG.Wait()

	for index, err := range errs {
		if err == nil || !strings.Contains(err.Error(), "tcaplus unavailable") {
			t.Fatalf("waiter[%d] err=%v, want injected load failure", index, err)
		}
	}
	runtime.mu.Lock()
	left := runtime.actors[playerID]
	runtime.mu.Unlock()
	if left != nil {
		t.Fatal("failed actor remained in Runtime.actors")
	}

	store.rejecting.Store(false)
	a, err := runtime.actorFor(context.Background(), playerID, LocalOwnerEpoch)
	if err != nil {
		t.Fatalf("retry actorFor: %v", err)
	}
	response, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, snapshotRequest(playerID, "retry-snap"))
	if err != nil || response.GetError() != nil {
		t.Fatalf("retry snapshot failed: err=%v response=%v", err, response)
	}
	if a.lifecycle.Load() != actorLifecycleReady {
		t.Fatal("retry actor not ready")
	}
	if store.loadCount.Load() < 2 {
		t.Fatalf("Store.Load calls = %d, want at least 2 (fail + retry)", store.loadCount.Load())
	}
}

func TestRuntimeFailedActivationCannotRemoveReplacementActor(t *testing.T) {
	const playerID = uint64(9004)
	runtime := NewRuntime()
	defer runtime.Close()

	oldLoadCtx, oldCancel := context.WithCancel(context.Background())
	old := &runtimeActor{
		mailbox:    actor.NewMailbox(1),
		loadCtx:    oldLoadCtx,
		loadCancel: oldCancel,
	}
	old.lifecycle.Store(actorLifecycleFailed)
	old.setActivationErr(errors.New("old failure"))

	replacementLoadCtx, replacementCancel := context.WithCancel(context.Background())
	defer replacementCancel()
	replacement := &runtimeActor{
		mailbox:    actor.NewMailbox(1),
		state:      NewDevelopmentState(playerID),
		loadCtx:    replacementLoadCtx,
		loadCancel: replacementCancel,
	}
	replacement.lifecycle.Store(actorLifecycleReady)

	runtime.mu.Lock()
	runtime.actors[playerID] = replacement
	runtime.mu.Unlock()

	runtime.removeActorIfSame(playerID, old)

	runtime.mu.Lock()
	got := runtime.actors[playerID]
	runtime.mu.Unlock()
	if got != replacement {
		t.Fatal("removeActorIfSame deleted the replacement actor")
	}
	old.mailbox.Close()
	replacement.mailbox.Close()
}

func TestRuntimeActivationRejectsStaleOwnerEpoch(t *testing.T) {
	const playerID = uint64(9005)
	state := NewDevelopmentState(playerID)
	state.OwnerEpoch = 2
	store := &recordingCheckpointStore{state: state}
	runtime, err := NewRuntimeWithStore(store)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	_, err = runtime.actorFor(context.Background(), playerID, 1)
	if !errors.Is(err, ErrNotOwner) {
		t.Fatalf("actorFor = %v, want ErrNotOwner", err)
	}
	runtime.mu.Lock()
	left := runtime.actors[playerID]
	runtime.mu.Unlock()
	if left != nil {
		t.Fatal("stale-owner activation left an actor behind")
	}
}

func TestRuntimeActorLoadingBackpressure(t *testing.T) {
	const playerID = uint64(9006)
	state := NewDevelopmentState(playerID)
	store := newBlockingCheckpointStore(state)
	runtime, err := NewRuntimeWithStore(store)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	creatorDone := make(chan error, 1)
	go func() {
		_, err := runtime.actorFor(context.Background(), playerID, LocalOwnerEpoch)
		creatorDone <- err
	}()
	select {
	case <-store.started:
	case <-time.After(3 * time.Second):
		t.Fatal("Store.Load was not entered")
	}

	runtime.mu.Lock()
	a := runtime.actors[playerID]
	runtime.mu.Unlock()
	if a == nil {
		t.Fatal("loading actor missing")
	}

	const queued = 64
	var wg sync.WaitGroup
	wg.Add(queued)
	for i := 0; i < queued; i++ {
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = a.mailbox.Do(ctx, func() {})
		}()
	}
	time.Sleep(50 * time.Millisecond)

	overflowCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	overflowErr := a.mailbox.Do(overflowCtx, func() {})
	if !errors.Is(overflowErr, context.DeadlineExceeded) {
		t.Fatalf("overflow enqueue = %v, want deadline exceeded", overflowErr)
	}

	close(store.release)
	wg.Wait()
	if err := <-creatorDone; err != nil {
		t.Fatalf("creator: %v", err)
	}
}

func TestRuntimeDrainWhileActorIsLoading(t *testing.T) {
	const playerID = uint64(9007)
	state := NewDevelopmentState(playerID)
	store := newBlockingCheckpointStore(state)
	runtime, err := NewRuntimeWithStore(store)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	creatorDone := make(chan error, 1)
	go func() {
		_, err := runtime.actorFor(context.Background(), playerID, LocalOwnerEpoch)
		creatorDone <- err
	}()
	select {
	case <-store.started:
	case <-time.After(3 * time.Second):
		t.Fatal("Store.Load was not entered")
	}

	shardID := routing.ShardForPlayer(playerID)
	drainDone := make(chan error, 1)
	go func() {
		_, err := runtime.DrainShardForMigration(context.Background(), shardID, LocalOwnerEpoch)
		drainDone <- err
	}()

	select {
	case err := <-drainDone:
		if err != nil {
			t.Fatalf("drain while loading: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("drain blocked while actor was loading")
	}

	runtime.mu.Lock()
	left := len(runtime.actors)
	runtime.mu.Unlock()
	if left != 0 {
		t.Fatalf("actors after drain = %d, want 0", left)
	}
	select {
	case err := <-creatorDone:
		if err == nil {
			t.Fatal("creator actorFor succeeded after drain canceled load")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("creator actorFor did not return after drain")
	}
}

func TestRuntimeCloseWhileActorIsLoading(t *testing.T) {
	const playerID = uint64(9008)
	state := NewDevelopmentState(playerID)
	store := newBlockingCheckpointStore(state)
	runtime, err := NewRuntimeWithStore(store)
	if err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := runtime.actorFor(context.Background(), playerID, LocalOwnerEpoch)
		errCh <- err
	}()
	select {
	case <-store.started:
	case <-time.After(3 * time.Second):
		t.Fatal("Store.Load was not entered")
	}

	closed := make(chan struct{})
	go func() {
		runtime.Close()
		close(closed)
	}()

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Runtime.Close blocked while actor was loading")
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("actorFor succeeded after Close during load")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("actorFor did not return after Close")
	}
}

// initialCheckpointFake 模拟 NotFound → CreateInitial 的 Zone 侧路径。
type initialCheckpointFake struct {
	mu            sync.Mutex
	state         *State
	token         StoreToken
	loadCount     atomic.Int64
	createCount   atomic.Int64
	createStatus  CheckpointWriteStatus
	createErr     error
	loadErr       error
	blockCreate   chan struct{}
	createStarted chan struct{}
}

func (s *initialCheckpointFake) Load(_ context.Context, playerID uint64) (LoadedCheckpoint, error) {
	s.loadCount.Add(1)
	if s.loadErr != nil {
		return LoadedCheckpoint{}, s.loadErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil || s.state.PlayerID != playerID {
		return LoadedCheckpoint{}, ErrCheckpointNotFound
	}
	return LoadedCheckpoint{
		State:             s.state,
		PersistedRevision: s.state.CheckpointRevision,
		Token:             cloneStoreToken(s.token),
	}, nil
}

func (s *initialCheckpointFake) SaveCAS(_ context.Context, write CheckpointWrite) (CheckpointWriteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := StateFromCheckpoint(write.Checkpoint)
	if err != nil {
		return CheckpointWriteResult{Status: CheckpointWriteCorruptConflict}, err
	}
	s.state = state
	if len(s.token) == 0 {
		s.token = StoreToken("tok-1")
	}
	return CheckpointWriteResult{
		Status: CheckpointWriteApplied, NewToken: cloneStoreToken(s.token),
	}, nil
}

func (s *initialCheckpointFake) CreateInitial(
	_ context.Context,
	checkpoint *datav1.PlayerCheckpointV1,
) (CheckpointWriteResult, error) {
	s.createCount.Add(1)
	if s.createStarted != nil {
		select {
		case <-s.createStarted:
		default:
			close(s.createStarted)
		}
	}
	if s.blockCreate != nil {
		<-s.blockCreate
	}
	if s.createErr != nil {
		status := s.createStatus
		if status == 0 {
			status = CheckpointWriteRetryableFailure
		}
		return CheckpointWriteResult{Status: status}, s.createErr
	}
	status := s.createStatus
	if status == 0 {
		status = CheckpointWriteApplied
	}
	if status == CheckpointWriteFenced ||
		status == CheckpointWriteCorruptConflict ||
		status == CheckpointWriteRetryableFailure {
		return CheckpointWriteResult{Status: status}, s.createErr
	}
	state, err := StateFromCheckpoint(checkpoint)
	if err != nil {
		return CheckpointWriteResult{Status: CheckpointWriteCorruptConflict}, err
	}
	s.mu.Lock()
	s.state = state
	if len(s.token) == 0 {
		s.token = StoreToken("initial-token")
	}
	token := cloneStoreToken(s.token)
	s.mu.Unlock()
	return CheckpointWriteResult{Status: status, NewToken: token}, nil
}

func TestRuntimeCreatesInitialCheckpointOnFirstActivation(t *testing.T) {
	const playerID = uint64(9101)
	store := &initialCheckpointFake{}
	runtime, err := NewRuntimeWithStore(store)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	fixedNow := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	runtime.now = func() time.Time { return fixedNow }

	a, err := runtime.actorFor(context.Background(), playerID, LocalOwnerEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if a.lifecycle.Load() != actorLifecycleReady {
		t.Fatal("actor not ready after initial create")
	}
	if store.createCount.Load() != 1 || store.loadCount.Load() != 1 {
		t.Fatalf("load=%d create=%d, want 1/1", store.loadCount.Load(), store.createCount.Load())
	}
	if a.state.PlayerID != playerID ||
		a.state.Coins != InitialCoinBalance ||
		a.state.Inventory[BasicFertilizerID] != 1 ||
		len(a.state.Plots) != int(InitialPlotCount) ||
		a.state.OwnerEpoch != LocalOwnerEpoch ||
		a.persistedRevision != 1 ||
		string(a.persistedToken) != "initial-token" {
		t.Fatalf("unexpected initial actor state: coins=%d plots=%d epoch=%d rev=%d token=%q",
			a.state.Coins, len(a.state.Plots), a.state.OwnerEpoch,
			a.persistedRevision, a.persistedToken)
	}
	if a.state.ChapterID != InitialChapterID || len(a.state.Tasks) == 0 {
		t.Fatalf("initial chapter = %d tasks=%d", a.state.ChapterID, len(a.state.Tasks))
	}
}

func TestRuntimeDoesNotBecomeReadyBeforeInitialCheckpointIsDurable(t *testing.T) {
	const playerID = uint64(9102)
	store := &initialCheckpointFake{
		createStarted: make(chan struct{}),
		blockCreate:   make(chan struct{}),
	}
	runtime, err := NewRuntimeWithStore(store)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	errCh := make(chan error, 1)
	go func() {
		_, err := runtime.actorFor(context.Background(), playerID, LocalOwnerEpoch)
		errCh <- err
	}()
	select {
	case <-store.createStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("CreateInitial was not entered")
	}
	runtime.mu.Lock()
	actor := runtime.actors[playerID]
	lifecycle := actorLifecycleLoading
	if actor != nil {
		lifecycle = actor.lifecycle.Load()
	}
	runtime.mu.Unlock()
	if lifecycle != actorLifecycleLoading {
		t.Fatalf("lifecycle during create = %d, want Loading", lifecycle)
	}
	close(store.blockCreate)
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeInitializesNewPlayerOnceUnderConcurrency(t *testing.T) {
	const playerID = uint64(9103)
	const concurrency = 100
	store := &initialCheckpointFake{}
	runtime, err := NewRuntimeWithStore(store)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	var startWG, doneWG sync.WaitGroup
	startWG.Add(concurrency)
	doneWG.Add(concurrency)
	began := make(chan struct{})
	errs := make([]error, concurrency)
	for i := 0; i < concurrency; i++ {
		go func(index int) {
			defer doneWG.Done()
			startWG.Done()
			<-began
			_, errs[index] = runtime.actorFor(
				context.Background(), playerID, LocalOwnerEpoch,
			)
		}(i)
	}
	startWG.Wait()
	close(began)
	doneWG.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("actorFor[%d] = %v", index, err)
		}
	}
	if store.loadCount.Load() != 1 || store.createCount.Load() != 1 {
		t.Fatalf("load=%d create=%d, want 1/1", store.loadCount.Load(), store.createCount.Load())
	}
	runtime.mu.Lock()
	count := len(runtime.actors)
	runtime.mu.Unlock()
	if count != 1 {
		t.Fatalf("actors = %d, want 1", count)
	}
}

func TestRuntimeNeverTreatsStoreFailureAsCheckpointNotFound(t *testing.T) {
	const playerID = uint64(9104)
	store := &initialCheckpointFake{loadErr: errors.New("tcaplus unavailable")}
	runtime, err := NewRuntimeWithStore(store)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	_, err = runtime.actorFor(context.Background(), playerID, LocalOwnerEpoch)
	if err == nil {
		t.Fatal("expected activation failure")
	}
	if store.createCount.Load() != 0 {
		t.Fatalf("CreateInitial calls = %d, want 0", store.createCount.Load())
	}
}

func TestRuntimeReconcilesAmbiguousInitialCheckpointCreate(t *testing.T) {
	const playerID = uint64(9105)
	expected := NewInitialCheckpoint(playerID, time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC))
	expected.OwnerEpoch = LocalOwnerEpoch
	state, err := StateFromCheckpoint(expected)
	if err != nil {
		t.Fatal(err)
	}
	store := &initialCheckpointFake{
		createStatus: CheckpointWriteRetryableFailure,
		createErr:    errors.New("response lost"),
	}
	// Create 返回不确定错误后，对账 Load 能读到已写入的相同内容。
	store.state = state
	store.token = StoreToken("reconciled")
	store.loadErr = nil
	// 第一次 Load（激活）需要 NotFound；Create 失败后再 Load 对账。
	var loads atomic.Int64
	reconciling := &reconcileInitialStore{
		inner: store,
		loads: &loads,
	}
	runtime, err := NewRuntimeWithStore(reconciling)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.now = func() time.Time {
		return time.UnixMilli(expected.CreatedAtMs).UTC()
	}

	a, err := runtime.actorFor(context.Background(), playerID, LocalOwnerEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if string(a.persistedToken) != "reconciled" {
		t.Fatalf("persisted token = %q", a.persistedToken)
	}
	if store.createCount.Load() != 1 {
		t.Fatalf("createCount = %d", store.createCount.Load())
	}
}

// repairingCheckpointStore mimics the Tcaplus durable store: it hands back a
// state one revision ahead of the persisted row because loading pruned Outbox
// entries the Relay already delivered.
type repairingCheckpointStore struct {
	state  *State
	behind bool
	saves  atomic.Int64
}

func (s *repairingCheckpointStore) Load(_ context.Context, playerID uint64) (LoadedCheckpoint, error) {
	if s.state == nil || s.state.PlayerID != playerID {
		return LoadedCheckpoint{}, ErrCheckpointNotFound
	}
	persisted := s.state.CheckpointRevision
	if s.behind {
		persisted++
	} else {
		s.state.CheckpointRevision++
	}
	return LoadedCheckpoint{State: s.state, PersistedRevision: persisted}, nil
}

func (s *repairingCheckpointStore) SaveCAS(context.Context, CheckpointWrite) (CheckpointWriteResult, error) {
	s.saves.Add(1)
	return CheckpointWriteResult{Status: CheckpointWriteApplied}, nil
}

// TestActivationAcceptsRepairedCheckpointRevision covers the gift sender who
// could never log in again: the durable store prunes a delivered Outbox entry,
// reports the repair as a higher revision, and activation used to reject that
// as corruption — permanently, since every retry repeats the same prune.
func TestActivationAcceptsRepairedCheckpointRevision(t *testing.T) {
	const playerID = uint64(9301)
	state := NewDevelopmentState(playerID)
	base := state.CheckpointRevision
	store := &repairingCheckpointStore{state: state}
	runtime, err := NewRuntimeWithStore(store)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	a, err := runtime.actorFor(context.Background(), playerID, LocalOwnerEpoch)
	if err != nil {
		t.Fatalf("actorFor = %v, want nil", err)
	}
	if a.state.CheckpointRevision != base+1 {
		t.Fatalf("state revision = %d, want %d", a.state.CheckpointRevision, base+1)
	}
	if a.persistedRevision != base {
		t.Fatalf("persisted revision = %d, want %d", a.persistedRevision, base)
	}
}

// TestActivationRejectsStaleCheckpointState keeps the corruption guard: a state
// older than the row it came from must never be served.
func TestActivationRejectsStaleCheckpointState(t *testing.T) {
	const playerID = uint64(9302)
	store := &repairingCheckpointStore{state: NewDevelopmentState(playerID), behind: true}
	runtime, err := NewRuntimeWithStore(store)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	_, err = runtime.actorFor(context.Background(), playerID, LocalOwnerEpoch)
	if err == nil || !strings.Contains(err.Error(), "behind the persisted revision") {
		t.Fatalf("actorFor = %v, want behind-the-persisted-revision failure", err)
	}
}

type legacyFarmStore struct {
	state *State
	saves atomic.Int64
}

func (s *legacyFarmStore) Load(_ context.Context, playerID uint64) (LoadedCheckpoint, error) {
	if s.state == nil || s.state.PlayerID != playerID {
		return LoadedCheckpoint{}, ErrCheckpointNotFound
	}
	return LoadedCheckpoint{State: s.state, PersistedRevision: s.state.CheckpointRevision}, nil
}

func (s *legacyFarmStore) SaveCAS(context.Context, CheckpointWrite) (CheckpointWriteResult, error) {
	s.saves.Add(1)
	return CheckpointWriteResult{Status: CheckpointWriteApplied}, nil
}

// TestActivationBackfillsPlotsAddedByLaterBuild covers accounts created when
// the starting farm was smaller: they must gain the new plots as empty land,
// keep the plots they already planted, and persist the result.
func TestActivationBackfillsPlotsAddedByLaterBuild(t *testing.T) {
	const playerID = uint64(9303)
	const legacyPlotCount = uint32(4)
	state := NewDevelopmentState(playerID)
	for plotID := InitialPlotID + legacyPlotCount; plotID < InitialPlotID+InitialPlotCount; plotID++ {
		delete(state.Plots, plotID)
	}
	keptPlot := state.Plots[InitialPlotID]
	base := state.CheckpointRevision
	store := &legacyFarmStore{state: state}
	runtime, err := NewRuntimeWithStore(store)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	a, err := runtime.actorFor(context.Background(), playerID, LocalOwnerEpoch)
	if err != nil {
		t.Fatalf("actorFor = %v, want nil", err)
	}
	if len(a.state.Plots) != int(InitialPlotCount) {
		t.Fatalf("plots = %d, want %d", len(a.state.Plots), InitialPlotCount)
	}
	if a.state.Plots[InitialPlotID] != keptPlot {
		t.Fatal("existing plot was replaced by the backfill")
	}
	if a.state.CheckpointRevision != base+1 || a.persistedRevision != base {
		t.Fatalf("revision = %d persisted = %d, want %d/%d",
			a.state.CheckpointRevision, a.persistedRevision, base+1, base)
	}
	if err := runtime.flushDirty(context.Background()); err != nil {
		t.Fatalf("flushDirty = %v, want nil", err)
	}
	if store.saves.Load() != 1 {
		t.Fatalf("saves = %d, want 1", store.saves.Load())
	}
}

type reconcileInitialStore struct {
	inner *initialCheckpointFake
	loads *atomic.Int64
}

func (s *reconcileInitialStore) Load(ctx context.Context, playerID uint64) (LoadedCheckpoint, error) {
	n := s.loads.Add(1)
	if n == 1 {
		return LoadedCheckpoint{}, ErrCheckpointNotFound
	}
	s.inner.mu.Lock()
	defer s.inner.mu.Unlock()
	return LoadedCheckpoint{
		State:             s.inner.state,
		PersistedRevision: s.inner.state.CheckpointRevision,
		Token:             cloneStoreToken(s.inner.token),
	}, nil
}

func (s *reconcileInitialStore) SaveCAS(ctx context.Context, write CheckpointWrite) (CheckpointWriteResult, error) {
	return s.inner.SaveCAS(ctx, write)
}

func (s *reconcileInitialStore) CreateInitial(
	ctx context.Context,
	checkpoint *datav1.PlayerCheckpointV1,
) (CheckpointWriteResult, error) {
	return s.inner.CreateInitial(ctx, checkpoint)
}
